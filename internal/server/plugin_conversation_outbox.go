package server

import (
	"context"
	"errors"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
)

const (
	// conversationDeliveryBatch bounds one pump pass. Only one delivery per
	// conversation is ever claimed, so this bounds concurrent conversations.
	conversationDeliveryBatch = 32
	// conversationRetryBase and conversationRetryMax bound the retry schedule:
	// 5s doubling to a 5 minute ceiling. A provider asking for a longer wait
	// (Slack's Retry-After) is absorbed inside the plugin, which knows the
	// provider's limits; the host only guarantees the backoff stays bounded.
	conversationRetryBase = 5 * time.Second
	conversationRetryMax  = 5 * time.Minute
	// conversationMaxAttempts is where a poison reply stops consuming the
	// conversation's ordering slot and becomes a visible dead letter.
	conversationMaxAttempts = 6
	// conversationPumpInterval wakes deliveries whose backoff has elapsed. A
	// fresh reply kicks the pump directly, so this is only the retry clock and
	// the recovery path after a restart.
	conversationPumpInterval = 5 * time.Second
	// conversationPruneInterval drops delivered receipts past their retention.
	conversationPruneInterval = time.Hour
	// conversationOutboxPrefix namespaces the wire operation id derived from a
	// delivery's immutable row id. The id is stable across retries, which is
	// what lets a plugin recognize a repeat and not post a second time.
	conversationOutboxPrefix = "conv-out:"
)

func conversationOutboxOperation(id int64) string {
	return conversationOutboxPrefix + strconv.FormatInt(id, 10)
}

// conversationRetryDelay is exponential with a hard ceiling. attempts is the
// number of failures already recorded, so the first retry waits the base.
func conversationRetryDelay(attempts int) time.Duration {
	delay := conversationRetryBase
	for range attempts {
		if delay >= conversationRetryMax/2 {
			return conversationRetryMax
		}
		delay *= 2
	}
	return delay
}

// conversationDeliveryWorker delivers outbox rows. The key is the ordering
// group — one provider conversation — so replies in one thread are strictly
// ordered while unrelated threads are delivered concurrently and a failing
// conversation cannot hold anyone else's replies.
func (s *Server) conversationDeliveryWorker() *worker.Worker[state.PluginConversationDelivery] {
	s.conversationOnce.Do(s.buildConversations)
	return s.conversationDeliveries
}

// runConversationDeliveryPump is the retry clock and the crash-recovery path:
// every unacknowledged row left by a disconnect or a restart is claimed again
// here. One immediate pass runs at startup, before the first tick.
func (s *Server) runConversationDeliveryPump(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	tick := time.NewTicker(conversationPumpInterval)
	defer tick.Stop()
	prune := time.NewTicker(conversationPruneInterval)
	defer prune.Stop()
	s.pumpConversationOutbox(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.pumpConversationOutbox(ctx)
		case <-prune.C:
			if err := s.stateDB.PrunePluginConversationOutbox(ctx); err != nil {
				log.WithError(err).Warn("pruning delivered conversation replies")
			}
		}
	}
}

// pumpConversationOutbox enqueues every due head delivery that is not already
// in flight. The in-flight set is what keeps a pump tick during a slow send
// from posting the same reply twice.
func (s *Server) pumpConversationOutbox(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	claimed, err := s.stateDB.ClaimPluginConversationReplies(ctx, conversationDeliveryBatch)
	if err != nil {
		log.WithError(err).Warn("claiming conversation replies for delivery")
		return
	}
	allowed := map[string]bool{}
	for _, delivery := range claimed {
		id := delivery.Key.PluginID
		if _, asked := allowed[id]; !asked {
			allowed[id] = s.conversationDeliveryAllowed(ctx, id)
		}
		if allowed[id] && s.beginConversationDelivery(delivery.ID) {
			s.conversationDeliveryWorker().Enqueue(delivery)
		}
	}
}

// conversationDeliveryAllowed reports whether a plugin may be delivered to right
// now. Disabling an instance or revoking its grant has to stop delivery at once
// — and stop it without spending the reply's retry budget, because an operator
// disabling a plugin for an hour would otherwise return to find every owed
// reply turned into a dead letter. An unauthorized delivery is left untouched
// in its outbox: not attempted, not failed, not discarded, and picked up by the
// next pump tick once the plugin is enabled and granted again. Re-enabling
// therefore resumes owed work rather than replaying anything that was denied.
//
// Fails closed: an unreadable registration pauses instead of posting.
func (s *Server) conversationDeliveryAllowed(ctx context.Context, pluginID string) bool {
	// Lock order matches the broker's authorization: server lifecycle, then
	// durable state.
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if s.stateDB == nil {
		return false
	}
	return s.stateDB.WithPluginConversation(ctx, pluginID,
		func(d plugins.Description, grants []string, _ plugins.ConversationConfig) error {
			return plugins.ConversationAllowed(d, grants)
		}) == nil
}

// kickConversationOutbox pumps without waiting for the next tick, so a fresh
// reply and the next reply in a conversation both go out immediately.
func (s *Server) kickConversationOutbox() {
	go runWithRecover("plugin-conversation-pump", func() {
		s.pumpConversationOutbox(context.Background())
	})
}

func (s *Server) beginConversationDelivery(id int64) bool {
	s.conversationOnce.Do(s.buildConversations)
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	if s.conversationInFlight[id] {
		return false
	}
	s.conversationInFlight[id] = true
	return true
}

func (s *Server) endConversationDelivery(id int64) {
	s.conversationMu.Lock()
	delete(s.conversationInFlight, id)
	s.conversationMu.Unlock()
}

// deliverConversationReply attempts one delivery and then acknowledges or
// records the failure. Send-then-acknowledge is deliberate: a crash in that
// window replays the delivery, so the guarantee is at-least-once for the send
// and the plugin is responsible for not repeating a post it already made.
func (s *Server) deliverConversationReply(delivery state.PluginConversationDelivery) {
	defer s.endConversationDelivery(delivery.ID)
	ctx := context.Background()
	fields := log.Fields{
		"plugin_id": delivery.Key.PluginID, "delivery_id": delivery.ID, "attempts": delivery.Attempts,
	}
	err := s.conversations().Reply(ctx, delivery.Key.PluginID, conversationOutboxOperation(delivery.ID),
		plugins.ConversationReply{AccountID: delivery.Key.AccountID, ThreadID: delivery.Key.ThreadID, Text: delivery.Text})
	if err == nil {
		if ack := s.stateDB.AckPluginConversationReply(ctx, delivery.ID); ack != nil {
			// The post happened; only the receipt failed. The replay is a
			// duplicate risk the plugin's own idempotency covers.
			log.WithError(ack).WithFields(fields).Warn("acknowledging a delivered conversation reply")
		}
		// Advance the conversation: its next reply was held behind this one.
		s.kickConversationOutbox()
		return
	}
	dead, failErr := s.stateDB.FailPluginConversationReply(ctx, delivery.ID,
		conversationFailureReason(err), conversationRetryDelay(delivery.Attempts), conversationMaxAttempts)
	if failErr != nil {
		log.WithError(failErr).WithFields(fields).Warn("recording a failed conversation reply")
		return
	}
	if dead {
		log.WithError(err).WithFields(fields).Error("conversation reply exhausted its retries; awaiting retry or discard")
		return
	}
	log.WithError(err).WithFields(fields).Warn("retrying conversation reply delivery")
}

// conversationFailureReason is the operator-facing cause shown next to a dead
// letter. Wire errors are a closed category set and plugin payloads are never
// logged, so nothing here can carry provider content or credentials.
func conversationFailureReason(err error) string {
	var wire *plugins.WireError
	if errors.As(err, &wire) {
		return string(wire.Category)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return string(plugins.ErrorDeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return string(plugins.ErrorCancelled)
	case errors.Is(err, plugins.ErrUnavailable):
		return string(plugins.ErrorUnavailable)
	case errors.Is(err, state.ErrPluginState), errors.Is(err, state.ErrPluginNotFound):
		return "host state error"
	}
	return string(plugins.ErrorInternal)
}

// conversationBacklogPaused reports whether inbound conversation work must
// pause. It fails closed: an unreadable backlog pauses rather than admitting a
// message whose reply the host may not be able to hold.
func (s *Server) conversationBacklogPaused(ctx context.Context, pluginID string) error {
	err := s.stateDB.PluginConversationBacklogFull(ctx, pluginID)
	if err == nil {
		return nil
	}
	if errors.Is(err, state.ErrPluginConversationBacklogFull) {
		log.WithField("plugin_id", pluginID).
			Warn("pausing conversation intake: the reply delivery backlog is at its limit")
	} else {
		log.WithError(err).WithField("plugin_id", pluginID).Warn("reading conversation delivery backlog")
	}
	return plugins.ErrUnavailable
}

// manageConversationBacklog serves the read and the two controls behind the
// plugin management routes, so they inherit the same owner routing and the
// localhost requirement that every other privileged plugin control has. The
// caller holds pluginMu.
func (s *Server) manageConversationBacklog(ctx context.Context, pluginID, action string, input pluginManagementInput) (any, error) {
	switch action {
	case "conversations":
		return s.stateDB.PluginConversationBacklogStatus(ctx, pluginID)
	case "conversations/retry":
		if input.DeliveryID <= 0 {
			return nil, state.ErrPluginInvalid
		}
		if err := s.stateDB.RetryPluginConversationReply(ctx, pluginID, input.DeliveryID); err != nil {
			return nil, err
		}
		s.kickConversationOutbox()
	case "conversations/discard":
		if input.DeliveryID <= 0 {
			return nil, state.ErrPluginInvalid
		}
		if err := s.stateDB.DiscardPluginConversationReply(ctx, pluginID, input.DeliveryID); err != nil {
			return nil, err
		}
		// Discarding unblocks the conversation's next reply.
		s.kickConversationOutbox()
	default:
		return nil, state.ErrPluginInvalid
	}
	return s.stateDB.PluginConversationBacklogStatus(ctx, pluginID)
}
