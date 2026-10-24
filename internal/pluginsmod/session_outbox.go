package pluginsmod

import (
	"context"
	"errors"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

// sessionOutboxPrefix namespaces the wire operation id derived from a row's
// immutable id. The id is stable across retries, which is what lets a plugin
// recognize a repeat and not act on the turn twice.
const sessionOutboxPrefix = "sess-out:"

func sessionOutboxOperation(id int64) string {
	return sessionOutboxPrefix + strconv.FormatInt(id, 10)
}

func (m *Module) newSessionOutbox() *outbox[PluginSessionCompletion] {
	return &outbox[PluginSessionCompletion]{
		name:    "session",
		claim:   m.claimSessionCompletions,
		row:     func(c PluginSessionCompletion) (int64, string) { return c.ID, c.PluginID },
		allowed: m.sessionDeliveryAllowed,
		enqueue: func(c PluginSessionCompletion) { m.sessionDeliveryWorker().Enqueue(c) },
		prune: func(ctx context.Context) error {
			return m.plugins.PrunePluginSessionOutbox(ctx)
		},
		pruneFailed: "pruning delivered session completions",
		workers:     &m.workers,
	}
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
	prefix, err := pluginSessionReference(ctx, adapter, sessionID)
	if err != nil {
		return
	}
	delivery, err := m.plugins.AppendPluginSessionCompletion(ctx, pluginID, platformID, sessionID, prefix+":"+messageID, text, usage)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": pluginID, "session_id": sessionID}).
			Error("recording completed session turn for delivery")
		return
	}
	if delivery != 0 {
		m.sessionOutbox.kick()
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
	prefix, err := pluginSessionReference(ctx, adapter, sessionID)
	if err != nil {
		return false, err
	}
	return m.plugins.HasPluginSessionCompletion(ctx, pluginID, prefix+":"+lifecycle.LatestMessageID)
}

// claimSessionCompletions pages through due head completions until a batch
// has been admitted.
func (m *Module) claimSessionCompletions(ctx context.Context, admit func(PluginSessionCompletion) bool) {
	if m.store == nil {
		return
	}
	var after int64
	for admitted := 0; admitted < outboxDeliveryBatch && ctx.Err() == nil; {
		claimed, err := m.plugins.sessionCompletionPage(ctx, after, outboxDeliveryBatch)
		if err != nil {
			log.WithError(err).Warn("claiming session completions for delivery")
			return
		}
		for _, completion := range claimed {
			after = completion.ID
			if identity.IsPendingReference(completion.SessionID) {
				continue
			}
			if admit(completion) {
				admitted++
				if admitted == outboxDeliveryBatch {
					return
				}
			}
		}
		if len(claimed) < outboxDeliveryBatch {
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
		func(d plugins.Description, grants []string, _ plugins.Config) error {
			return plugins.SessionAllowed(d, grants)
		}) == nil
}

// DeliverSessionCompletion attempts one delivery and then acknowledges or
// records the failure. Send-then-acknowledge is deliberate: a crash in that
// window replays the delivery, so the guarantee is at-least-once for the call
// and the plugin recognizes the repeat by the stable operation id.
func (m *Module) DeliverSessionCompletion(completion PluginSessionCompletion) {
	defer m.sessionOutbox.end(completion.ID)
	m.mu.Lock()
	ctx := m.workerCtx
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	m.deliverSessionCompletion(ctx, completion)
}

func (m *Module) deliverSessionCompletion(ctx context.Context, completion PluginSessionCompletion) {
	if identity.IsPendingReference(completion.SessionID) {
		return
	}
	fields := log.Fields{"plugin_id": completion.PluginID, "delivery_id": completion.ID, "attempts": completion.Attempts}
	current, err := m.plugins.PluginSessionCompletionCurrent(ctx, completion.ID, completion.Attempts)
	if err != nil {
		log.WithError(err).WithFields(fields).Warn("checking a claimed session completion")
	}
	if !current {
		return
	}
	wireID, err := m.pluginWireSession(ctx, completion.PlatformID, completion.SessionID)
	if err != nil {
		return // No broker delivery was attempted.
	}
	err = m.completePluginSession(ctx, completion, wireID)
	// Settle an admitted attempt durably even if shutdown cancelled the call.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err == nil {
		if ack := m.plugins.AckPluginSessionCompletion(writeCtx, completion.ID); ack != nil {
			log.WithError(ack).WithFields(fields).Warn("acknowledging a delivered session completion")
		}
		m.sessionOutbox.kick()
		return
	}
	dead, failErr := m.plugins.FailPluginSessionCompletion(writeCtx, completion.ID,
		conversationFailureReason(err), outboxRetryDelay(completion.Attempts), outboxMaxAttempts)
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
