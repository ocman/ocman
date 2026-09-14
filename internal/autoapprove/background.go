package autoapprove

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/safety"
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
	// Keep the first complete asked snapshot for durable attribution and
	// tool-call correlation; permission.replied carries only IDs and reply.
	asked := s.rememberAskedForEnsure(string(platformID), sessionID, permissionID, permission, patterns, metadata)

	// Read the configured delay once so both the cache anchor and the
	// goroutine's sleep use the same value. The goroutine re-reads it
	// inside backgroundAutoApprove for cases where the setting was
	// changed between the asked event and the judge starting.
	delayMs := s.judgeDelayMs.Load()
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
			asked,
		)
	}()
}

// backgroundAutoApprove is the authoritative auto-approve engine.
// It fires whenever an SSE permission.asked event is observed on an
// OpenCode event stream — either via the frontend-driven /event tee in
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
	asked askedPermission,
) {
	permission, patterns, metadata := asked.permission, asked.patterns, asked.metadata
	logger := log.WithFields(log.Fields{
		"sessionID":    sessionID,
		"permissionID": permissionID,
	})
	asked, enabled := s.prepareAutoApprove(ctx, platformID, sessionID, permissionID, asked)
	logger.WithFields(log.Fields{
		"enabled":            enabled,
		"autoApproveDefault": s.deps.DefaultEnabled,
	}).Debug("background auto-approve: checking enabled state")
	if !enabled {
		logger.Debug("background auto-approve: disabled, skipping")
		return
	}
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{})
	s.persistRecordedManualLifecycle(asked, sessionID, permissionID)
	if s.shortCircuitAutoApprove(ctx, platformID, adapter, sessionID, permissionID, asked, logger) {
		return
	}
	inputs, err := s.resolveJudgeInputs(ctx, asked.directory, asked.directoryErr, sessionID)
	if err != nil {
		s.setLifecycleMethod(asked, sessionID, permissionID, state.PermissionEvaluationJudge, state.PermissionEvaluationError)
		logger.WithError(err).Warn("background auto-approve: could not resolve session directory")
		s.handleUnsafeVerdict(sessionID, permissionID, JudgeResult{Verdict: verdictUnsafe, Reasoning: "auto-approve could not resolve the session directory"})
		return
	}
	s.setLifecycleMethod(asked, sessionID, permissionID, state.PermissionEvaluationJudge, "")
	if inputs.delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(inputs.delay) * time.Millisecond):
		}
	}
	s.markAutoApproveChecking(sessionID, permissionID)
	logger.Info("background auto-approve: judging permission")
	inputs.sections = s.resolveJudgeSections(ctx, inputs.directory, sessionID)
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		JudgeStartedAt:   time.Now().UnixMilli(),
		EvaluationMethod: state.PermissionEvaluationJudge,
	})
	result := s.judge.JudgeWithCallback(ctx, inputs.directory, permission, patterns, metadata, inputs.sections, s.emitChecking(sessionID, permissionID))
	s.completeJudgeLifecycle(asked, sessionID, permissionID, result)
	if ctx.Err() != nil {
		logger.WithField("ctxErr", ctx.Err()).Debug("background auto-approve: cancelled before result could be applied")
		return
	}
	logger.WithFields(log.Fields{
		"verdict":        string(result.Verdict),
		"judgeSessionID": result.SessionID,
	}).Debug("background auto-approve: judge returned")
	if result.Verdict != verdictSafe {
		s.handleUnsafeVerdict(sessionID, permissionID, result)
		return
	}
	s.recordJudgedWithReasoning(sessionID, permissionID, result.Verdict, result.Reasoning)
	if hash := permissionHash(permission, patterns, metadata); hash != "" {
		s.recordSafeCommandVerdict(sessionID, hash, result.Reasoning)
	}
	s.respondAndPersistSafeApproval(platformID, adapter, sessionID, permissionID, asked, result.Reasoning, logger)
}

type judgeInputs struct {
	directory string
	delay     int64
	sections  []PromptSection
}

func (s *Service) prepareAutoApprove(ctx context.Context, platformID platforms.ID, sessionID, permissionID string, asked askedPermission) (askedPermission, bool) {
	if asked.lifecycleObserved {
		return asked, asked.lifecycleEnabled
	}
	enabled := s.deps.DefaultEnabled
	if s.deps.Store != nil {
		if perSession, exists, err := s.deps.Store.GetAutoApprove(ctx, string(platformID), sessionID); err == nil && exists {
			enabled = perSession
		}
	}
	var directory string
	var directoryErr error
	if enabled {
		directory, directoryErr = s.ResolveSessionDir(sessionID)
	}
	return s.observeAskedLifecycle(sessionID, permissionID, asked, enabled, directory, directoryErr), enabled
}

func (s *Service) shortCircuitAutoApprove(ctx context.Context, platformID platforms.ID, adapter platforms.Platform, sessionID, permissionID string, asked askedPermission, logger *log.Entry) bool {
	if reason := deniedReason(asked.permission, asked.patterns, asked.metadata); reason != "" {
		reason = "blocked by ocman's hard denylist: " + reason
		s.setLifecycleMethod(asked, sessionID, permissionID, state.PermissionEvaluationDenylist, state.PermissionEvaluationDenylisted)
		logger.WithField("reason", reason).Warn("background auto-approve: refusing, command is on the hard denylist")
		s.handleUnsafeVerdict(sessionID, permissionID, JudgeResult{Verdict: verdictUnsafe, Reasoning: reason})
		return true
	}
	hash := permissionHash(asked.permission, asked.patterns, asked.metadata)
	if hash == "" {
		return false
	}
	cachedReason, ok := s.lookupInheritedSafeCommandVerdict(ctx, sessionID, hash)
	if !ok {
		return false
	}
	s.setLifecycleMethod(asked, sessionID, permissionID, state.PermissionEvaluationCache, state.PermissionEvaluationCacheSafe)
	logger.WithField("hash", hash).Debug("background auto-approve: safe-command cache hit, skipping judge")
	reason := "cached: " + cachedReason
	s.recordJudgedWithReasoning(sessionID, permissionID, verdictSafe, reason)
	s.respondAndPersistSafeApproval(platformID, adapter, sessionID, permissionID, asked, reason, logger)
	return true
}

func (s *Service) resolveJudgeInputs(ctx context.Context, directory string, directoryErr error, sessionID string) (judgeInputs, error) {
	if directoryErr != nil {
		return judgeInputs{}, directoryErr
	}
	inputs := judgeInputs{directory: directory, delay: s.judgeDelayMs.Load()}
	if s.deps.Store != nil {
		if delay, err := s.deps.Store.GetJudgeDelayMs(ctx); err == nil {
			inputs.delay = delay
		}
		if provider, modelID, ok := loadJudgeModel(ctx, s.deps.Store); ok && s.judge != nil {
			s.judge.setModel(provider, modelID)
		}
	}
	return inputs, nil
}

func (s *Service) resolveJudgeSections(ctx context.Context, directory, sessionID string) []PromptSection {
	var sections []PromptSection
	if s.deps.Store != nil {
		if stored, err := s.deps.Store.GetPromptSections(ctx); err == nil {
			for _, section := range stored {
				sections = append(sections, PromptSection{Title: section.Title, Content: section.Content, Enabled: section.Enabled})
			}
		}
	}
	if s.judge == nil || s.judge.openCodePort == nil {
		return sections
	}
	port := s.judge.openCodePort(directory)
	if port == "" {
		return sections
	}
	messages := s.judge.recentUserMessages(ctx, port, sessionID)
	if len(messages) == 0 {
		return sections
	}
	var content strings.Builder
	content.WriteString("The user recently sent these messages (oldest first):\n")
	for _, message := range messages {
		if len(message) > 300 {
			message = message[:300] + "…"
		}
		content.WriteString("  - " + message + "\n")
	}
	content.WriteString("\nIf the permission request is a direct and proportionate consequence of what the user asked for, lean toward SAFE.")
	return append(sections, PromptSection{Title: "Recent user intent", Content: content.String()})
}

func (s *Service) emitChecking(sessionID, permissionID string) func(string) {
	return func(string) {
		payload, err := json.Marshal(map[string]string{"permissionId": permissionID, "sessionID": sessionID})
		if err == nil {
			s.emitSessionSseEvent(sessionID, "ocman.permission.checking", payload)
		}
	}
}

func (s *Service) handleUnsafeVerdict(sessionID, permissionID string, result JudgeResult) {
	s.recordJudgedWithReasoning(sessionID, permissionID, result.Verdict, result.Reasoning)
	s.emitFlagged(sessionID, permissionID, result.Reasoning)
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
	sessionID, permissionID string,
	asked askedPermission,
	reasoning string,
	logger *log.Entry,
) {
	permission, patterns := asked.permission, asked.patterns
	respondCtx, respondCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer respondCancel()
	s.beginAIResponse(sessionID, permissionID)
	if err := adapter.RespondPermission(respondCtx, platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        "once",
	}); err != nil {
		s.finishAIResponse(sessionID, permissionID, false)
		logger.WithError(err).Warn("background auto-approve: failed to respond to permission")
		s.recordJudgedWithReasoning(sessionID, permissionID, verdictUnsafe, "auto-approve could not submit its approval")
		s.emitFlagged(sessionID, permissionID, "auto-approve could not submit its approval")
		return
	}
	if !s.finishAIResponse(sessionID, permissionID, true) {
		logger.Debug("background auto-approve: user response won permission race")
		return
	}

	approvedAt := time.Now().UnixMilli()
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		ResolvedAt: approvedAt,
		Resolution: state.PermissionResolutionAutoApproved,
	})

	// Persist the approval so the UI notice survives a page refresh.
	// JudgeSessionID is intentionally written as the empty string: the
	// judge session has already been deleted by JudgeWithCallback (or
	// never existed for cache-hit approvals). The column is retained
	// for backwards-compat with rows written before the cleanup
	// change so existing notices keep rendering, but new rows leave
	// it empty.
	if s.deps.Store != nil {
		if err := s.deps.Store.RecordApprovedPermission(
			respondCtx,
			string(platformID),
			sessionID,
			state.ApprovedPermission{
				PermissionID:   permissionID,
				PermissionText: permission,
				Patterns:       patterns,
				JudgeSessionID: "",
				Reasoning:      reasoning,
				ApprovedBy:     state.ApprovalActorAI,
				Reply:          state.ApprovalReplyOnce,
				Metadata:       asked.metadata,
				AskedAt:        asked.askedAt,
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
		"approvedBy":   "ai",
		"reply":        "once",
		"metadata":     asked.metadata,
		"askedAt":      asked.askedAt,
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

	logger.Debug("background auto-approve: permission approved")
}

// deniedReason reports why a permission request is hard-denied, or ""
// when nothing on the denylist matches. It scans the human-readable
// permission text, the patterns, and every string-valued entry in the
// tool metadata (the Bash command, an Edit/Write file path, a Webfetch
// URL) so the denylist covers non-Bash tools too.
func deniedReason(permission string, patterns []string, metadata map[string]any) string {
	texts := make([]string, 0, len(patterns)+len(metadata)+1)
	texts = append(texts, permission)
	texts = append(texts, patterns...)
	// Sorted so the reported reason is deterministic across runs.
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok {
			texts = append(texts, value)
		}
	}
	return safety.DeniedAny(texts...)
}

// emitFlagged tells connected clients that a permission was left for
// the human, with the one-line reason. Broadcast as well as
// session-scoped so a background session surfaces in the bell / favicon
// / toast immediately instead of waiting for the next notify poll.
func (s *Service) emitFlagged(sessionID, permissionID, reasoning string) {
	payload, err := json.Marshal(map[string]string{
		"permissionId": permissionID,
		"sessionID":    sessionID,
		"reasoning":    reasoning,
	})
	if err != nil {
		return
	}
	s.emitSessionSseEvent(sessionID, "ocman.permission.flagged", payload)
	s.broadcastGlobalEvent("ocman.permission.flagged", payload)
}

// SurfacePermissionNotification repeats a fast unsafe verdict after the
// platform has recorded the prompt, closing the event-order race where the
// first flagged broadcast arrived before the prompt became listable.
func (s *Service) SurfacePermissionNotification(sessionID, permissionID string) {
	status, ok := s.lookupAutoApproveStatus(sessionID, permissionID)
	if ok && status.verdict == verdictUnsafe && status.manualResolvedAt == 0 {
		s.emitFlagged(sessionID, permissionID, status.reasoning)
	}
}
