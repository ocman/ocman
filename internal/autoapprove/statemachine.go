package autoapprove

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"

	log "github.com/sirupsen/logrus"
)

// --- Auto-approve per-permission state tracking ---

// autoApproveStatus is the per-permission state remembered for the
// lifetime of the ocman process. A non-nil cancel means a judge
// goroutine is still running; a non-empty verdict means the judge
// finished. The two are mutually exclusive only in steady state — a
// running judge transitions from (cancel non-nil, verdict "") to
// (cancel nil, verdict non-empty) when recordJudged is called.
//
// judgeStartsAt and checking exist so a freshly-connected SSE sink
// (the headless-watcher case where the watcher claimed the permission
// before the frontend was open) can replay the most recent applicable
// ocman.permission.* event when Ensure short-circuits.
type autoApproveStatus struct {
	// cancel cancels the judge goroutine's context. Non-nil while the
	// goroutine is running; cleared by releaseAutoApprove.
	cancel context.CancelFunc

	// judgeStartsAt is the wall-clock Unix-ms at which the judge will
	// start running (i.e. now + configured delay). Used to replay
	// ocman.permission.pending with a stable countdown anchor when a
	// frontend connects mid-delay.
	judgeStartsAt int64

	// checking indicates whether the configured delay has elapsed and
	// the judge is actually running. Toggled by markAutoApproveChecking
	// after the delay sleep finishes.
	checking bool

	// verdict is the final verdict once the judge has finished. Empty
	// while the judge is still running.
	verdict judgeVerdict

	// reasoning is the one-line conclusion extracted from the judge's
	// JSON response. Populated when verdict is non-empty. Surfaced to
	// the UI on the ocman.permission.flagged event so the user sees
	// *why* the judge made its call.
	reasoning string
}

// autoApproveKey is the registry key for a single permission record.
func autoApproveKey(sessionID, permissionID string) string {
	return sessionID + "|" + permissionID
}

// claimAutoApprove atomically registers a new judge run for
// (sessionID, permissionID) and returns a cancellable context derived
// from parent. The second return value is true if the claim was
// granted; false if another goroutine is already handling this
// permission, OR a verdict for it has already been recorded.
//
// Callers that already know the judgeStartsAt anchor (the standard
// Ensure path) should use claimAutoApproveWithStart so the
// anchor is stored for later replay. This helper exists for symmetry
// with the pre-watcher API and defaults judgeStartsAt to 0.
func (s *Service) claimAutoApprove(parent context.Context, sessionID, permissionID string) (context.Context, bool) {
	return s.claimAutoApproveWithStart(parent, sessionID, permissionID, 0)
}

// claimAutoApproveWithStart is claimAutoApprove plus a judgeStartsAt
// anchor. The anchor is stored on the status record so a later
// short-circuit replay can re-emit ocman.permission.pending with the
// same value, letting the frontend resume the countdown without a
// fresh clock.
func (s *Service) claimAutoApproveWithStart(parent context.Context, sessionID, permissionID string, judgeStartsAt int64) (context.Context, bool) {
	if s == nil {
		// Nil-Server only happens in test setups that don't exercise
		// cancellation; return the parent unchanged.
		return parent, true
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	if existing := s.autoApprove[key]; existing != nil {
		// Either still in flight or already judged — either way the
		// caller must not start a second goroutine. Replay logic in
		// Ensure handles the bring-up of any newly-connected
		// sink.
		return nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	s.autoApprove[key] = &autoApproveStatus{
		cancel:        cancel,
		judgeStartsAt: judgeStartsAt,
	}
	return ctx, true
}

// markAutoApproveChecking flips the status's checking flag, signalling
// that the configured delay has elapsed and the judge is now running.
// Used by the replay path to choose between emitting
// ocman.permission.pending (still waiting) and .checking (judge
// active). No-op if no status exists for the permission.
func (s *Service) markAutoApproveChecking(sessionID, permissionID string) {
	if s == nil {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if st := s.autoApprove[key]; st != nil {
		st.checking = true
	}
	s.autoApproveMu.Unlock()
}

// releaseAutoApprove invokes the registered cancel func and clears it
// on the status record. The status itself is retained so a later
// Ensure call for the same permissionID can still replay
// the recorded verdict (or, for an in-flight goroutine that exits
// before a verdict is recorded, recognise that the slot is free for
// a fresh claim).
//
// Idempotent — safe to call after Cancel. Must be called by
// the goroutine that successfully claimed the entry, typically in a
// deferred block.
func (s *Service) releaseAutoApprove(sessionID, permissionID string) {
	if s == nil {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	st := s.autoApprove[key]
	if st == nil {
		s.autoApproveMu.Unlock()
		return
	}
	cancel := st.cancel
	st.cancel = nil
	// If the judge exited without recording a verdict (cancelled
	// before completion, panic, etc.) drop the record so a fresh
	// permission with the same key can claim again. Recorded
	// verdicts are kept so REST resurrection can replay them.
	if st.verdict == "" {
		delete(s.autoApprove, key)
	}
	s.autoApproveMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Cancel signals the in-flight judge for (sessionID,
// permissionID) to abort. No-op if there is no judge running. The
// goroutine sees ctx.Done() at its next select point and drops the
// result. The status record is left in place — releaseAutoApprove
// (from the goroutine's defer) is what evicts it. This way two
// cancels in quick succession don't fight a re-entry; the slot only
// frees when the goroutine actually exits.
//
// Returns true if a cancel was sent (something was running), false
// if there was nothing to cancel.
func (s *Service) Cancel(sessionID, permissionID string) bool {
	if s == nil {
		return false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	st := s.autoApprove[key]
	var cancel context.CancelFunc
	if st != nil {
		cancel = st.cancel
	}
	s.autoApproveMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// recordJudged persists the verdict for (sessionID, permissionID) so a
// later Ensure call for the same OpenCode permission ID
// short-circuits instead of running the judge again. Called by
// backgroundAutoApprove on every terminal path (safe or unsafe).
//
// Equivalent to recordJudgedWithReasoning with reasoning="". Retained
// so call-sites that don't have reasoning handy stay readable.
func (s *Service) recordJudged(sessionID, permissionID string, verdict judgeVerdict) {
	s.recordJudgedWithReasoning(sessionID, permissionID, verdict, "")
}

// recordJudgedWithReasoning is recordJudged plus the one-line reasoning
// extracted from the judge's response. Surfaced to the UI on the
// ocman.permission.flagged event during replay so users see *why* the
// judge said unsafe.
func (s *Service) recordJudgedWithReasoning(sessionID, permissionID string, verdict judgeVerdict, reasoning string) {
	if s == nil || permissionID == "" {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	st := s.autoApprove[key]
	if st == nil {
		st = &autoApproveStatus{}
		s.autoApprove[key] = st
	}
	st.verdict = verdict
	st.reasoning = reasoning
	s.autoApproveMu.Unlock()
}

// lookupJudged returns the cached verdict for (sessionID, permissionID)
// and ok=true if the judge already produced a verdict in this process.
// Pure read of the verdict field; the status may still be in flight if
// verdict is empty (in which case ok is false).
func (s *Service) lookupJudged(sessionID, permissionID string) (judgeVerdict, bool) {
	if s == nil {
		return "", false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	st := s.autoApprove[key]
	if st == nil || st.verdict == "" {
		return "", false
	}
	return st.verdict, true
}

// lookupAutoApproveStatus returns a snapshot copy of the current status
// for (sessionID, permissionID) and ok=true if any state is recorded
// (in-flight OR judged). The returned struct is a value copy so callers
// can read fields without holding the mutex. Returns the zero status
// and ok=false when no record exists.
func (s *Service) lookupAutoApproveStatus(sessionID, permissionID string) (autoApproveStatus, bool) {
	if s == nil {
		return autoApproveStatus{}, false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	st := s.autoApprove[key]
	if st == nil {
		return autoApproveStatus{}, false
	}
	return *st, true
}

// --- Per-session safe-command cache ---
//
// The autoApprove map above caches verdicts by the OpenCode-generated
// permissionID, so resurrecting *the same prompt* short-circuits the
// judge. But every fresh permission for the same logical action (e.g.
// the user running `pnpm test` five times in a row) gets a new
// permissionID, so the user pays for the judge each time.
//
// The safe-command cache fills that gap: when the judge returns "safe"
// for a Bash command, we additionally remember the verdict keyed by
// md5(metadata["command"]) inside the session. The next time the same
// raw command shows up — even with a different permissionID — we skip
// the LLM and respond "once" immediately.
//
// Only **safe** verdicts are cached. Unsafe verdicts always re-run
// through the judge so the user gets fresh reasoning if a flagged
// command resurfaces (and so a one-off "unsafe" classification can't
// permanently block a benign command).
//
// Per-session scope: the same command in a different session goes
// through the judge again. This keeps the cache narrow and avoids
// surprising cross-session approvals.
//
// In-memory, process lifetime — cleared on restart. The persisted
// ApprovedPermission DB rows cover audit and notice replay; this
// cache is purely a performance optimisation.

// commandHash returns the md5 hex of metadata["command"] when present
// and non-empty, or "" otherwise. Empty means "not cacheable" — callers
// must not record or look up against an empty hash.
//
// Only Bash permission requests carry a "command" key; all other tools
// (Edit/Write/Webfetch/…) return "" and fall through to the judge on
// every request. This matches the design constraint that the cache is
// keyed on the *exact* command string, which only makes sense for
// shell commands.
func commandHash(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["command"]
	if !ok {
		return ""
	}
	cmd, ok := raw.(string)
	if !ok || cmd == "" {
		return ""
	}
	sum := md5.Sum([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

// lookupSafeCommandVerdict returns the cached safe-verdict reasoning
// for (sessionID, hash) and ok=true if an entry exists. Returns
// ("", false) on miss, nil receiver, or empty hash.
func (s *Service) lookupSafeCommandVerdict(sessionID, hash string) (string, bool) {
	if s == nil || hash == "" {
		return "", false
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		return "", false
	}
	reasoning, ok := bySession[hash]
	return reasoning, ok
}

// maxParentWalk caps how far up the parent chain
// lookupInheritedSafeCommandVerdict walks. A child spawns a grandchild
// rarely; the cap keeps a malformed/cyclic parent link from looping
// forever without needing a visited-set.
const maxParentWalk = 8

// lookupInheritedSafeCommandVerdict is lookupSafeCommandVerdict plus
// parent inheritance: on a miss for sessionID it walks up the
// child->parent chain (via the ParentSessionID dep) and returns the
// first ancestor's cached safe-verdict for the same command hash. This
// lets a child session auto-approve a command the parent already had
// approved — no fresh judge run, no user prompt.
//
// The returned reasoning is prefixed with "inherited from parent: " so
// the origin is visible in the UI and DB audit row. Falls back to the
// plain single-session behaviour when no resolver is wired.
func (s *Service) lookupInheritedSafeCommandVerdict(sessionID, hash string) (string, bool) {
	if s == nil || hash == "" {
		return "", false
	}
	// Own session first — no prefix, it's a direct hit.
	if reasoning, ok := s.lookupSafeCommandVerdict(sessionID, hash); ok {
		return reasoning, true
	}
	cur := sessionID
	for i := 0; i < maxParentWalk; i++ {
		parent, ok := s.ResolveParentSessionID(cur)
		if !ok || parent == "" || parent == cur {
			return "", false
		}
		if reasoning, ok := s.lookupSafeCommandVerdict(parent, hash); ok {
			return "inherited from parent: " + reasoning, true
		}
		cur = parent
	}
	return "", false
}

// recordSafeCommandVerdict stores reasoning for (sessionID, hash) in
// the cache. No-op on nil receiver or empty hash. Overwrites any
// existing entry — the latest verdict wins.
func (s *Service) recordSafeCommandVerdict(sessionID, hash, reasoning string) {
	if s == nil || hash == "" {
		return
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	if s.safeCommandCache == nil {
		s.safeCommandCache = make(map[string]map[string]string)
	}
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		bySession = make(map[string]string)
		s.safeCommandCache[sessionID] = bySession
	}
	bySession[hash] = reasoning
}

// emitSessionSseEvent writes an SSE event to the currently-registered
// writer for sessionID. If no client is connected, the call is a no-op.
//
// The sink is resolved on every call (not captured at goroutine start)
// so a long-running judge whose client has disconnected mid-flight
// silently drops follow-up events. The sink itself has a closed flag
// guarded by its own mutex, so even a write that races with
// UnregisterSink cannot dereference a recycled http.ResponseWriter.
func (s *Service) emitSessionSseEvent(sessionID, eventType string, payload []byte) {
	s.lookupSink(sessionID).write(eventType, payload)
}

// emitPermissionPending writes the ocman.permission.pending SSE event
// to the registered writer for sessionID, if one exists. The event
// anchors the frontend countdown to an absolute wall-clock time so the
// remaining seconds are correct even if the client reconnects.
//
// judgeStartsAt is the Unix-ms moment the judge will start running
// (i.e. the moment the configured delay elapses). Passed in by callers
// so the same anchor used during the initial emit can be re-emitted on
// a replay — letting the frontend resume the countdown rather than
// restarting from zero.
//
// No-op when no client is listening; the judge runs anyway.
func (s *Service) emitPermissionPending(sessionID, permissionID string, judgeStartsAt int64) {
	// sessionID matches OpenCode's wire-format casing so the frontend
	// reducer's eventSessionId() correctly routes this event to the
	// reducer for `sessionID`. Without this, ocman events would be
	// applied to whichever session reducer is currently rendered.
	payload, err := json.Marshal(map[string]interface{}{
		"permissionId":  permissionID,
		"sessionID":     sessionID,
		"judgeStartsAt": judgeStartsAt,
	})
	if err != nil {
		return
	}
	log.WithFields(log.Fields{
		"permissionID":  permissionID,
		"sessionID":     sessionID,
		"judgeStartsAt": judgeStartsAt,
	}).Info("emitting ocman.permission.pending")
	s.emitSessionSseEvent(sessionID, "ocman.permission.pending", payload)
}

// replayAutoApproveState emits the most recent applicable
// ocman.permission.* event for an already-known permission to the
// currently-registered SSE sink.
//
// Why this exists: the headless autoApproveWatcher subscribes to
// OpenCode's /event stream from server startup, so it routinely
// observes (and claims, judges, or completes) permission.asked events
// before any frontend tab is open. When the user later opens the
// session, the REST resurrection path calls Ensure again —
// which short-circuits because the work is already done. Without this
// replay, the just-registered SSE sink would never receive the
// pending / checking / flagged / auto-approved events that drive the
// countdown UI, leaving the prompt frozen.
//
// The frontend reducer is idempotent against repeat events (it dedups
// on permissionId / judgeStartsAt), so a replay during the same tab's
// lifetime is harmless.
func (s *Service) replayAutoApproveState(sessionID, permissionID, permission string, patterns []string) {
	st, ok := s.lookupAutoApproveStatus(sessionID, permissionID)
	if !ok {
		return
	}
	switch {
	case st.verdict == verdictSafe:
		// Safe verdicts: nothing to replay over SSE. OpenCode has
		// already cleared the prompt via RespondPermission, so the
		// REST /permissions list won't even include it on a fresh
		// page load. The approval notice is injected into the
		// message stream by injectApprovalNotices when the session
		// detail loads.
		return
	case st.verdict == verdictUnsafe:
		// Unsafe verdicts: the prompt stays pending for the human, so
		// the frontend needs the flagged reasoning to render the
		// "AI flagged this" annotation.
		if st.reasoning == "" {
			return
		}
		payload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
			"reasoning":    st.reasoning,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.flagged", payload)
	case st.checking:
		// Judge is currently running — emit checking so the UI shows
		// a spinner instead of a frozen countdown.
		payload, err := json.Marshal(map[string]string{
			"permissionId": permissionID,
			"sessionID":    sessionID,
		})
		if err != nil {
			return
		}
		s.emitSessionSseEvent(sessionID, "ocman.permission.checking", payload)
	default:
		// Still in the pre-judge delay — emit pending with the original
		// judgeStartsAt anchor so the countdown resumes from the right
		// remaining time.
		s.emitPermissionPending(sessionID, permissionID, st.judgeStartsAt)
	}
}
