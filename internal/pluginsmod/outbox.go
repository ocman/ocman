package pluginsmod

import (
	"context"
	"errors"
	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
)

// conversationOutboxPrefix namespaces the wire operation id derived from a
// delivery's immutable row id. The id is stable across retries, which is what
// lets a plugin recognize a repeat and not post a second time.
const conversationOutboxPrefix = "conv-out:"

func conversationOutboxOperation(id int64) string {
	return conversationOutboxPrefix + strconv.FormatInt(id, 10)
}

// conversationDeliveryWorker delivers outbox rows. The key is the ordering
// group — one provider conversation — so replies in one thread are strictly
// ordered while unrelated threads are delivered concurrently and a failing
// conversation cannot hold anyone else's replies.
func (m *Module) conversationDeliveryWorker() *worker.Worker[state.PluginConversationDelivery] {
	m.conversationOnce.Do(m.buildConversations)
	return m.conversationDeliveries
}

func (m *Module) newConversationOutbox() *outbox[state.PluginConversationDelivery] {
	return &outbox[state.PluginConversationDelivery]{
		name: "conversation",
		claim: func(ctx context.Context, admit func(state.PluginConversationDelivery) bool) {
			if m.store == nil {
				return
			}
			claimed, err := m.store.ClaimPluginConversationReplies(ctx, outboxDeliveryBatch)
			if err != nil {
				log.WithError(err).Warn("claiming conversation replies for delivery")
				return
			}
			for _, delivery := range claimed {
				admit(delivery)
			}
		},
		row:     func(d state.PluginConversationDelivery) (int64, string) { return d.ID, d.Key.PluginID },
		allowed: m.conversationDeliveryAllowed,
		enqueue: func(d state.PluginConversationDelivery) { m.conversationDeliveryWorker().Enqueue(d) },
		prune: func(ctx context.Context) error {
			return m.store.PrunePluginConversationOutbox(ctx)
		},
		pruneFailed: "pruning delivered conversation replies",
		workers:     &m.workers,
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
func (m *Module) conversationDeliveryAllowed(ctx context.Context, pluginID string) bool {
	// Lock order matches the broker's authorization: module lifecycle (mu), then
	// durable state.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return false
	}
	return m.store.WithPluginConversation(ctx, pluginID,
		func(d plugins.Description, grants []string, _ plugins.Config) error {
			return plugins.ConversationAllowed(d, grants)
		}) == nil
}

// deliverConversationReply attempts one delivery and then acknowledges or
// records the failure. Send-then-acknowledge is deliberate: a crash in that
// window replays the delivery, so the guarantee is at-least-once for the send
// and the plugin is responsible for not repeating a post it already made.
func (m *Module) deliverConversationReply(delivery state.PluginConversationDelivery) {
	defer m.conversationOutbox.end(delivery.ID)
	ctx := context.Background()
	fields := log.Fields{
		"plugin_id": delivery.Key.PluginID, "delivery_id": delivery.ID, "attempts": delivery.Attempts,
	}
	// A pump may have claimed this row before a concurrent delivery settled it
	// and left the in-flight set; that stale claim must not post again.
	current, err := m.store.PluginConversationReplyCurrent(ctx, delivery.ID, delivery.Attempts)
	if err != nil {
		log.WithError(err).WithFields(fields).Warn("checking a claimed conversation reply")
	}
	if !current {
		return
	}
	err = m.Conversations().Reply(ctx, delivery.Key.PluginID, conversationOutboxOperation(delivery.ID),
		plugins.ConversationReply{AccountID: delivery.Key.AccountID, ThreadID: delivery.Key.ThreadID, Text: delivery.Text})
	if err == nil {
		if ack := m.store.AckPluginConversationReply(ctx, delivery.ID); ack != nil {
			// The post happened; only the receipt failed. The replay is a
			// duplicate risk the plugin's own idempotency covers.
			log.WithError(ack).WithFields(fields).Warn("acknowledging a delivered conversation reply")
		}
		// Advance the conversation: its next reply was held behind this one.
		m.conversationOutbox.kick()
		return
	}
	dead, failErr := m.store.FailPluginConversationReply(ctx, delivery.ID,
		conversationFailureReason(err), outboxRetryDelay(delivery.Attempts), outboxMaxAttempts)
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
func (m *Module) conversationBacklogPaused(ctx context.Context, pluginID string) error {
	err := m.store.PluginConversationBacklogFull(ctx, pluginID)
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
// caller holds mu.
func (m *Module) manageConversationBacklog(ctx context.Context, pluginID, action string, input pluginManagementInput) (any, error) {
	switch action {
	case "conversations":
		return m.store.PluginConversationBacklogStatus(ctx, pluginID)
	case "conversations/retry":
		if input.DeliveryID <= 0 {
			return nil, state.ErrPluginInvalid
		}
		if err := m.store.RetryPluginConversationReply(ctx, pluginID, input.DeliveryID); err != nil {
			return nil, err
		}
		m.conversationOutbox.kick()
	case "conversations/discard":
		if input.DeliveryID <= 0 {
			return nil, state.ErrPluginInvalid
		}
		if err := m.store.DiscardPluginConversationReply(ctx, pluginID, input.DeliveryID); err != nil {
			return nil, err
		}
		// Discarding unblocks the conversation's next reply.
		m.conversationOutbox.kick()
	default:
		return nil, state.ErrPluginInvalid
	}
	return m.store.PluginConversationBacklogStatus(ctx, pluginID)
}

// DeliverOwed claims every due reply, as a pump tick does, and waits until
// those deliveries have settled.
func (m *Module) DeliverOwed(ctx context.Context) error {
	m.conversationOutbox.pump(ctx)
	return m.conversationDeliveryWorker().Drain(ctx)
}
