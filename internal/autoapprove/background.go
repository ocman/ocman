package autoapprove

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// --- Background (server-side) auto-approve ---

// Ensure is the single entry point for kicking off the
// auto-approve pipeline for a given permission. It:
//
//  1. If this permission already has state (in-flight goroutine or a
//     recorded verdict), replays the most recent applicable
//     ocman.permission.* event to the SSE sink — this brings a
//     freshly-connected frontend up to date with work the headless
//     watcher has already done — and returns without starting a
//     second goroutine.
//  2. Otherwise computes the judge start anchor, claims the slot,
//     emits ocman.permission.pending so the countdown starts
//     immediately on any connected client, and launches
//     backgroundAutoApprove in a goroutine.
//
// Safe to call from any handler; safe to call multiple times for the
// same permission. backgroundAutoApprove looks up the SSE sink on each
// emit so client disconnects mid-judge are non-fatal.
func (s *Service) Ensure(
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID, permissionID, permission string,
	patterns []string,
	metadata map[string]any,
) {
	// Remember the asked-side permission text + patterns so a later
	// user-clicked "Allow always" reply can be persisted with them
	// (the permission.replied event carries neither). Issue #101.
	s.rememberAsked(string(platformID), sessionID, permissionID, permission, patterns)

	// Read the configured delay once so both the cache anchor and the
	// goroutine's sleep use the same value. The goroutine re-reads it
	// inside backgroundAutoApprove for cases where the setting was
	// changed between the asked event and the judge starting.
	delayMs := s.judgeDelayMs
	judgeStartsAt := time.Now().Add(time.Duration(delayMs) * time.Millisecond).UnixMilli()

	ctx, ok := s.claimAutoApproveWithStart(context.Background(), sessionID, permissionID, judgeStartsAt)
	if !ok {
		// Cache hit. Either another goroutine is already handling the
		// judge for this permission, or a verdict was recorded earlier
		// in this process. Replay the current state to the (possibly
		// just-registered) sink so the frontend's UI catches up.
		log.WithFields(log.Fields{
			"sessionID":    sessionID,
			"permissionID": permissionID,
		}).Debug("auto-approve: cache hit, replaying state to sink")
		s.replayAutoApproveState(sessionID, permissionID, permission, patterns)
		return
	}
	s.emitPermissionPending(sessionID, permissionID, judgeStartsAt)
	go func() {
		defer s.releaseAutoApprove(sessionID, permissionID)
		s.backgroundAutoApprove(
			ctx,
			platformID,
			adapter,
			sessionID,
			permissionID,
			permission,
			patterns,
			metadata,
		)
	}()
}

// backgroundAutoApprove is the authoritative auto-approve engine.
// It fires whenever an SSE permission.asked event is observed on an
// OpenCode /event stream — either via the frontend-driven tee in
// serveSessionEvents (active while a browser tab is open) or via the
// headless runAutoApproveWatcher (active for the lifetime of the
// ocman process). Both entry points funnel through Ensure,
// which deduplicates so the judge runs at most once per permission.
//
// When auto-approve is enabled for the session it:
//  1. Emits an "ocman.permission.checking" SSE event to any connected
//     clients so the UI can show a "checking" indicator immediately.
//  2. Loads the user-defined judge prompt sections from stateDB.
//  3. Runs the LLM judge.
//  4. If the verdict is SAFE, responds "once" directly to the running
//     OpenCode instance, persists the approval, and emits an
//     "ocman.permission.auto-approved" SSE event back to clients.
//
// SSE events are emitted via emitSessionSseEvent, which resolves the
// currently-registered sink on every call. A client disconnect between
// the judge starting and finishing is non-fatal — follow-up events are
// silently dropped.
//
// This function blocks (it calls judge.Judge which polls OpenCode) and
// must always be called in a goroutine.
func (s *Service) backgroundAutoApprove(
	ctx context.Context,
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID string,
	permissionID string,
	permission string,
	patterns []string,
	metadata map[string]any,
) {
	logger := log.WithFields(log.Fields{
		"sessionID":    sessionID,
		"permissionID": permissionID,
	})
	// Check auto-approve state: per-session override, then server default.
	enabled := s.deps.DefaultEnabled
	if s.deps.Store != nil {
		if perSession, exists, err := s.deps.Store.GetAutoApprove(string(platformID), sessionID); err == nil && exists {
			enabled = perSession
		}
	}
	logger.WithFields(log.Fields{
		"enabled":            enabled,
		"autoApproveDefault": s.deps.DefaultEnabled,
	}).Info("background auto-approve: checking enabled state")
	if !enabled {
		logger.Info("background auto-approve: disabled, skipping")
		return
	}

	// Safe-command cache short-circuit. When the *exact same* Bash
	// command was previously approved in this session — or in any
	// ancestor session, so a child inherits the parent's approvals —
	// skip the LLM judge and the configured delay entirely: respond
	// "once", persist the audit row, and emit the SSE notice. The
	// "cached: " prefix (plus "inherited from parent: " for an
	// ancestor hit) makes the origin visible in the UI and DB.
	//
	// commandHash returns "" for non-Bash tools (Edit/Write/Webfetch/…)
	// and for malformed metadata, so the cache is opt-in by data
	// shape — no Edit permission can ever auto-approve from this
	// cache, regardless of metadata content.
	if hash := commandHash(metadata); hash != "" {
		if cachedReason, ok := s.lookupInheritedSafeCommandVerdict(sessionID, hash); ok {
			logger.WithField("hash", hash).Info("background auto-approve: safe-command cache hit, skipping judge")
			finalReason := "cached: " + cachedReason
			s.recordJudgedWithReasoning(sessionID, permissionID, verdictSafe, finalReason)
			s.respondAndPersistSafeApproval(
				platformID, adapter,
				sessionID, permissionID, permission,
				patterns, finalReason,
				logger,
			)
			return
		}
	}

	// Resolve directory for port discovery.
	directory, err := s.ResolveSessionDir(sessionID)
	if err != nil {
		logger.WithError(err).Warn("background auto-approve: could not resolve session directory")
		return
	}

	// Read the configured delay. Default to 0 on any error so the judge
	// still fires rather than blocking indefinitely.
	// The pending event was already emitted synchronously by the tee's
	// onPermission callback using the cached delay; we re-read here to
	// ensure the actual sleep matches the persisted value.
	delayMs := s.judgeDelayMs
	if s.deps.Store != nil {
		if d, err := s.deps.Store.GetJudgeDelayMs(); err == nil {
			delayMs = d
		}
	}

	// Apply the configured judge model (if any) so the persisted
	// setting takes effect without a restart. Empty/unset falls back
	// to the judgeModel* constants seeded in newPermissionJudge.
	if s.judge != nil && s.deps.Store != nil {
		if provider, modelID, ok := loadJudgeModel(s.deps.Store); ok {
			s.judge.modelProvider = provider
			s.judge.modelID = modelID
		}
	}

	// Wait the configured delay before starting the judge, giving the
	// human a window to respond manually. The context carries the
	// judgeTimeout deadline so we don't wait past it.
	if delayMs > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		}
	}

	// Flip the status to "checking" so replayAutoApproveState can
	// route a freshly-connected sink to ocman.permission.checking
	// rather than a stale countdown.
	s.markAutoApproveChecking(sessionID, permissionID)

	logger.Info("background auto-approve: judging permission")

	// Load user-defined prompt sections from stateDB so headless runs
	// use the same custom rules as the settings page.
	var customSections []PromptSection
	if s.deps.Store != nil {
		if stored, err := s.deps.Store.GetPromptSections(); err == nil {
			for _, ps := range stored {
				customSections = append(customSections, PromptSection{
					Title:   ps.Title,
					Content: ps.Content,
					Enabled: ps.Enabled,
				})
			}
		}
	}

	// Build a user-intent section from recent messages in this session
	// so the judge can factor in what the user explicitly asked for.
	if s.judge != nil && s.judge.openCodePort != nil {
		port := s.judge.openCodePort(directory)
		if port != "" {
			msgs := s.judge.recentUserMessages(ctx, port, sessionID)
			if len(msgs) > 0 {
				var b strings.Builder
				b.WriteString("The user recently sent these messages (oldest first):\n")
				for _, m := range msgs {
					b.WriteString("  - ")
					// Truncate very long messages to keep the prompt concise.
					if len(m) > 300 {
						m = m[:300] + "…"
					}
					b.WriteString(m)
					b.WriteString("\n")
				}
				b.WriteString("\nIf the permission request is a direct and proportionate consequence of what the user asked for, lean toward SAFE.")
				customSections = append(customSections, PromptSection{
					Title:   "Recent user intent",
					Content: b.String(),
				})
			}
		}
	}

	// Run the judge. The judge creates a transient OpenCode session,
	// sends the prompt, collects the verdict, then deletes the session
	// before returning (see JudgeWithCallback). We emit "checking" as
	// soon as the session is created so the UI can show a spinner
	// immediately. The sink is resolved on every emit so a client
	// disconnect during the (potentially slow) judge run can't panic
	// on a recycled writer.
	//
	// The judge session ID is intentionally NOT included in the
	// payload: the session is deleted shortly after the verdict is
	// extracted, so a "view judge session" link would 404 by the time
	// the user clicked it. The one-line reasoning surfaced on the
	// flagged/approved events is the durable signal.
	emitChecking := func(_ string) {
		// sessionID (all caps) matches OpenCode's wire convention so the
		// frontend reducer routes this event to the correct session.
		checkingPayload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.checking", checkingPayload)
	}
	result := s.judge.JudgeWithCallback(ctx, directory, permission, patterns, metadata, customSections, emitChecking)

	// If the user replied to the permission (via ocman API or directly
	// in the OpenCode TUI) while the judge was running, the cancel
	// fired and ctx.Err() is non-nil. Drop the verdict entirely:
	// - no recordJudged (the verdict is moot — the permission is
	//   already resolved; if OpenCode resurrects it for any reason we
	//   want a fresh judge rather than a stale cached verdict)
	// - no RespondPermission (OpenCode would reject it anyway)
	// - no auto-approved/flagged SSE event (the user already saw the
	//   prompt clear via permission.replied)
	// - no DB row (a notice attached to a manually-resolved prompt
	//   would be misleading)
	if ctx.Err() != nil {
		logger.WithField("ctxErr", ctx.Err()).Info("background auto-approve: cancelled before result could be applied")
		return
	}

	// Record the verdict (and reasoning) so a later Ensure
	// call for the same permissionID (e.g. the user re-opens the
	// session and handleSessionPermissions resurrects it via REST)
	// short-circuits instead of paying for another judge run, and so
	// replayAutoApproveState can surface the flagged reasoning to a
	// newly-connected sink. Recorded regardless of verdict — unsafe
	// verdicts are the main reason this cache exists: safe verdicts
	// already auto-respond and the permission disappears from
	// OpenCode's pending list, but unsafe verdicts deliberately leave
	// the prompt pending for the human, so without this cache every
	// REST poll would re-judge.
	s.recordJudgedWithReasoning(sessionID, permissionID, result.Verdict, result.Reasoning)

	logger.WithFields(log.Fields{
		"verdict":        string(result.Verdict),
		"judgeSessionID": result.SessionID,
	}).Info("background auto-approve: judge returned")

	if result.Verdict != verdictSafe {
		// Notify connected clients so they can show the judge's one-line
		// reasoning on the permission prompt even when the AI flagged it
		// for human review. The judge session has already been deleted
		// (see JudgeWithCallback), so result.SessionID is always empty
		// and the payload no longer carries a link — only the reasoning.
		// We emit the event when there is something useful to show
		// (reasoning is the practical floor).
		if result.Reasoning != "" {
			flaggedPayload, err := json.Marshal(map[string]string{
				"permissionId": permissionID,
				"sessionID":    sessionID,
				"reasoning":    result.Reasoning,
			})
			if err == nil {
				s.emitSessionSseEvent(sessionID, "ocman.permission.flagged", flaggedPayload)
				// Broadcast so background sessions that the judge flagged
				// for human review surface in the bell / favicon / toast
				// immediately instead of waiting for the next notify poll.
				s.broadcastGlobalEvent("ocman.permission.flagged", flaggedPayload)
			}
		}
		return
	}

	// Populate the per-session safe-command cache so subsequent
	// permission.asked events for the same raw command (different
	// permissionID, same session) skip the judge entirely. Only
	// safe verdicts are cached — unsafe verdicts always re-judge so
	// the user gets fresh reasoning. Skipped when metadata has no
	// "command" key (non-Bash tools).
	if hash := commandHash(metadata); hash != "" {
		s.recordSafeCommandVerdict(sessionID, hash, result.Reasoning)
	}

	s.respondAndPersistSafeApproval(
		platformID, adapter,
		sessionID, permissionID, permission,
		patterns, result.Reasoning,
		logger,
	)
}

// respondAndPersistSafeApproval clears a pending permission in OpenCode
// (Reply="once"), persists an ApprovedPermission audit row, and emits
// ocman.permission.auto-approved to any connected SSE sink for the
// session.
//
// Shared between the live-verdict path (judge ran, returned safe) and
// the safe-command cache-hit path (judge skipped). Both paths produce
// identical user-visible outcomes — the only durable difference is the
// "cached: " prefix on `reasoning` for cache-hit rows.
//
// Uses a fresh context (not the caller's cancellable ctx) so a late
// user-reply race between the verdict and this call doesn't leave
// OpenCode without our response. Errors from the adapter are logged
// and swallowed — at worst the permission stays pending and the user
// answers it manually.
func (s *Service) respondAndPersistSafeApproval(
	platformID platforms.ID,
	adapter platforms.Platform,
	sessionID, permissionID, permission string,
	patterns []string,
	reasoning string,
	logger *log.Entry,
) {
	respondCtx, respondCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer respondCancel()
	if err := adapter.RespondPermission(respondCtx, platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        "once",
	}); err != nil {
		logger.WithError(err).Warn("background auto-approve: failed to respond to permission")
		return
	}

	approvedAt := time.Now().UnixMilli()

	// Persist the approval so the UI notice survives a page refresh.
	// JudgeSessionID is intentionally written as the empty string: the
	// judge session has already been deleted by JudgeWithCallback (or
	// never existed for cache-hit approvals). The column is retained
	// for backwards-compat with rows written before the cleanup
	// change so existing notices keep rendering, but new rows leave
	// it empty.
	if s.deps.Store != nil {
		if err := s.deps.Store.RecordApprovedPermission(
			string(platformID),
			sessionID,
			state.ApprovedPermission{
				PermissionID:   permissionID,
				PermissionText: permission,
				Patterns:       patterns,
				JudgeSessionID: "",
				Reasoning:      reasoning,
				ApprovedAt:     approvedAt,
			},
		); err != nil {
			logger.WithError(err).Warn("background auto-approve: failed to persist approval")
		}
	}

	// Notify connected clients so they can inject the notice immediately
	// without waiting for a page reload. No judgeSessionId in the
	// payload — the session no longer exists; the frontend reducer
	// already falls back to permissionId for the stable notice key.
	if patterns == nil {
		patterns = []string{}
	}
	approvedPayload, err := json.Marshal(map[string]interface{}{
		"permissionId": permissionID,
		"sessionID":    sessionID,
		"permission":   permission,
		"patterns":     patterns,
		"reasoning":    reasoning,
		"approvedAt":   approvedAt,
	})
	if err == nil {
		s.emitSessionSseEvent(sessionID, "ocman.permission.auto-approved", approvedPayload)
	}

	// Broadcast the resolution to *every* connected client (not just the
	// per-session SSE sink) so cross-page prompt toasts for this session
	// can clear immediately instead of lingering until the next
	// /api/sessions/notify poll.
	s.broadcastPermissionResolved(sessionID, permissionID, "auto-approved")

	logger.Info("background auto-approve: permission approved")
}
