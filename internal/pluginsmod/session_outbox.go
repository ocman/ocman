package pluginsmod

import (
	"context"
	"errors"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

const (
	// sessionDeliveryBatch bounds one pump pass. Only one completion per
	// session is ever claimed, so this bounds concurrent sessions.
	sessionDeliveryBatch = 32
	// sessionRetryBase and sessionRetryMax bound the retry schedule, exactly
	// as conversation replies do: 5s doubling to a 5 minute ceiling.
	sessionRetryBase = 5 * time.Second
	sessionRetryMax  = 5 * time.Minute
	// sessionMaxAttempts is where a poison completion stops consuming its
	// session's ordering slot and becomes a dead letter.
	sessionMaxAttempts = 6
	// sessionPumpInterval wakes deliveries whose backoff has elapsed; a fresh
	// completion kicks the pump directly, so this is the retry clock and the
	// recovery path after a restart.
	sessionPumpInterval = 5 * time.Second
	// sessionPruneInterval drops delivered receipts past their retention.
	sessionPruneInterval = time.Hour
	// sessionOutboxPrefix namespaces the wire operation id derived from a
	// row's immutable id. The id is stable across retries, which is what lets
	// a plugin recognize a repeat and not act on the turn twice.
	sessionOutboxPrefix = "sess-out:"
	// sessionReconcileInterval is the backstop for instances that omit
	// terminal idle/changed events, mirroring the conversation reconciliation.
	sessionReconcileInterval = 5 * time.Minute
)

func sessionOutboxOperation(id int64) string {
	return sessionOutboxPrefix + strconv.FormatInt(id, 10)
}

func sessionRetryDelay(attempts int) time.Duration {
	delay := sessionRetryBase
	for range attempts {
		if delay >= sessionRetryMax/2 {
			return sessionRetryMax
		}
		delay *= 2
	}
	return delay
}

// ReplyToSession records the session's completed assistant turn for delivery to
// the plugin that started it. Sessions with no plugin are ignored. The mapping
// is read from the database, so a turn that completes after a restart still
// finds its recipient. The turn is appended to the durable outbox rather than
// posted directly: a disconnect, a plugin crash or a host restart replays the
// delivery instead of losing a completed turn.
func (m *Module) ReplyToSession(ctx context.Context, platformID, sessionID string) {
	if m.store == nil || sessionID == "" {
		return
	}
	pluginID, linked, err := m.plugins.GetPluginSessionPlugin(ctx, platformID, sessionID)
	if err != nil || !linked {
		return
	}
	adapter, ok := m.deps.Adapter(ctx, platformID, sessionID)
	if !ok {
		return
	}
	if skip, err := m.sessionCompletionAlreadyRecorded(ctx, adapter, pluginID, sessionID); err != nil {
		log.WithError(err).WithField("session_id", sessionID).Warn("checking session completion receipt")
		return
	} else if skip {
		return
	}
	detail, err := adapter.Session(ctx, sessionID, conversationFetchLimit, 0)
	if err != nil || detail == nil {
		log.WithError(err).WithField("session_id", sessionID).Warn("reading completed turn for session delivery")
		return
	}
	messageID, text := latestAssistantText(detail.Messages, detail.Parts)
	// A session.changed event can arrive before session.idle. It is a recovery
	// path for a missed idle edge, never permission to publish a partial turn.
	if detail.Session == nil || !conversationReplyReady(detail.Session.Status) {
		return
	}
	if text == "" {
		return
	}
	// The message ID keys the append, so a repeated idle edge for the same
	// completed turn adds no second delivery.
	usage := completedUsage(detail.Messages, messageID)
	if detail.TotalMessages > len(detail.Messages) {
		usage = nil
	}
	delivery, err := m.plugins.AppendPluginSessionCompletion(ctx, pluginID, platformID, sessionID, sessionID+":"+messageID, text, usage)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": pluginID, "session_id": sessionID}).
			Error("recording completed session turn for delivery")
		return
	}
	if delivery != 0 {
		m.kickSessionOutbox()
	}
}

// sessionCompletionAlreadyRecorded reads only lifecycle and the durable receipt
// before fetching a transcript, mirroring the conversation check.
func (m *Module) sessionCompletionAlreadyRecorded(ctx context.Context, adapter platforms.Platform, pluginID, sessionID string) (bool, error) {
	reader, ok := adapter.(platforms.LifecycleReader)
	if !ok {
		return false, nil
	}
	lifecycle, err := reader.SessionLifecycle(ctx, sessionID)
	if errors.Is(err, platforms.ErrUnsupported) {
		return false, nil
	}
	if err != nil || lifecycle == nil {
		return false, err
	}
	if !conversationReplyReady(lifecycle.Status) {
		return true, nil
	}
	if lifecycle.LatestMessageRole != "assistant" || lifecycle.LatestMessageID == "" {
		return false, nil
	}
	return m.plugins.HasPluginSessionCompletion(ctx, pluginID, sessionID+":"+lifecycle.LatestMessageID)
}

// pumpSessionOutbox enqueues every due head completion that is not already in
// flight. The in-flight set keeps a pump tick during a slow send from posting
// the same turn twice.
func (m *Module) pumpSessionOutbox(ctx context.Context) {
	if m.store == nil {
		return
	}
	allowed := map[string]bool{}
	var after int64
	for admitted := 0; admitted < sessionDeliveryBatch && ctx.Err() == nil; {
		claimed, err := m.plugins.sessionCompletionPage(ctx, after, sessionDeliveryBatch)
		if err != nil {
			log.WithError(err).Warn("claiming session completions for delivery")
			return
		}
		for _, completion := range claimed {
			after = completion.ID
			id := completion.PluginID
			if _, asked := allowed[id]; !asked {
				allowed[id] = m.sessionDeliveryAllowed(ctx, id)
			}
			if allowed[id] && m.beginSessionDelivery(completion.ID) {
				m.sessionDeliveryWorker().Enqueue(completion)
				admitted++
				if admitted == sessionDeliveryBatch {
					return
				}
			}
		}
		if len(claimed) < sessionDeliveryBatch {
			return
		}
	}
}

// sessionDeliveryAllowed reports whether a plugin may be delivered to right
// now. Disabling an instance or revoking its grant stops delivery at once
// without spending the completion's retry budget: the row stays pending and
// unattempted, and the next tick after re-enablement delivers exactly the work
// already owed. Fails closed: an unreadable registration pauses.
func (m *Module) sessionDeliveryAllowed(ctx context.Context, pluginID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil || m.stopping {
		return false
	}
	return m.store.WithPluginConversation(ctx, pluginID,
		func(d plugins.Description, grants []string, _ plugins.ConversationConfig) error {
			return plugins.SessionAllowed(d, grants)
		}) == nil
}

func (m *Module) beginSessionDelivery(id int64) bool {
	m.sessionOnce.Do(m.buildSessions)
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if m.sessionInFlight[id] {
		return false
	}
	m.sessionInFlight[id] = true
	return true
}

func (m *Module) endSessionDelivery(id int64) {
	m.sessionMu.Lock()
	delete(m.sessionInFlight, id)
	m.sessionMu.Unlock()
}

// kickSessionOutbox pumps without waiting for the next tick.
func (m *Module) kickSessionOutbox() {
	m.workers.Go(func(ctx context.Context) {
		runWithRecover("plugin-session-pump", func() { m.pumpSessionOutbox(ctx) })
	})
}

// runSessionDeliveryPump is the retry clock and the crash-recovery path: every
// unacknowledged row left by a disconnect or a restart is claimed again here.
func (m *Module) runSessionDeliveryPump(ctx context.Context) {
	if m.store == nil {
		return
	}
	tick := time.NewTicker(sessionPumpInterval)
	defer tick.Stop()
	prune := time.NewTicker(sessionPruneInterval)
	defer prune.Stop()
	m.pumpSessionOutbox(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.pumpSessionOutbox(ctx)
		case <-prune.C:
			if err := m.plugins.PrunePluginSessionOutbox(ctx); err != nil {
				log.WithError(err).Warn("pruning delivered session completions")
			}
		}
	}
}

// DeliverSessionCompletion attempts one delivery and then acknowledges or
// records the failure. Send-then-acknowledge is deliberate: a crash in that
// window replays the delivery, so the guarantee is at-least-once for the call
// and the plugin recognizes the repeat by the stable operation id.
func (m *Module) DeliverSessionCompletion(completion PluginSessionCompletion) {
	defer m.endSessionDelivery(completion.ID)
	m.mu.Lock()
	ctx := m.workerCtx
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	m.deliverSessionCompletion(ctx, completion)
}

func (m *Module) deliverSessionCompletion(ctx context.Context, completion PluginSessionCompletion) {
	fields := log.Fields{"plugin_id": completion.PluginID, "delivery_id": completion.ID, "attempts": completion.Attempts}
	current, err := m.plugins.PluginSessionCompletionCurrent(ctx, completion.ID, completion.Attempts)
	if err != nil {
		log.WithError(err).WithFields(fields).Warn("checking a claimed session completion")
	}
	if !current {
		return
	}
	err = m.SessionBroker().Completed(ctx, completion.PluginID, sessionOutboxOperation(completion.ID),
		plugins.SessionCompleted{SessionID: completion.SessionID, Text: completion.Text, Usage: completion.Usage})
	// Settle an admitted attempt durably even if shutdown cancelled the call.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err == nil {
		if ack := m.plugins.AckPluginSessionCompletion(writeCtx, completion.ID); ack != nil {
			log.WithError(ack).WithFields(fields).Warn("acknowledging a delivered session completion")
		}
		m.kickSessionOutbox()
		return
	}
	dead, failErr := m.plugins.FailPluginSessionCompletion(writeCtx, completion.ID,
		conversationFailureReason(err), sessionRetryDelay(completion.Attempts), sessionMaxAttempts)
	if failErr != nil {
		log.WithError(failErr).WithFields(fields).Warn("recording a failed session completion")
		return
	}
	if dead {
		log.WithError(err).WithFields(fields).Error("session completion exhausted its retries")
		return
	}
	log.WithError(err).WithFields(fields).Warn("retrying session completion delivery")
}

// runSessionReplyReconciliation is the backstop for OpenCode instances that
// omit terminal idle/changed events. The outbox's session:message key makes
// recovery safe.
func (m *Module) runSessionReplyReconciliation(ctx context.Context) {
	m.ReconcileSessionCompletions(ctx)
	ticker := time.NewTicker(sessionReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.ReconcileSessionCompletions(ctx)
		}
	}
}

// ReconcileSessionCompletions records every linked session's completed turn
// that has no receipt yet.
func (m *Module) ReconcileSessionCompletions(ctx context.Context) {
	if m.store == nil {
		return
	}
	sessions, err := m.plugins.ListPluginSessionSessions(ctx)
	if err != nil {
		log.WithError(err).Warn("listing plugin sessions for completion reconciliation")
		return
	}
	for _, session := range sessions {
		m.ReplyToSession(ctx, session.PlatformID, session.SessionID)
	}
}
