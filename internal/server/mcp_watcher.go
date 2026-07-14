package server

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	// childSessionWatchInterval is how often the watcher polls for
	// child session completion.
	childSessionWatchInterval = 5 * time.Second
)

// childSessionWatchTickFn is the per-tick body of runChildSessionWatcher,
// lifted to a package-level variable so tests can inject a fake.
var childSessionWatchTickFn = func(s *Server) { s.checkAndInjectChildResults(context.Background()) }

// runChildSessionWatcher is a background goroutine that polls state.db
// for child sessions in non-terminal states, checks their completion
// status against OpenCode's DB, updates state.db, and injects result
// messages into parent sessions when a child completes.
//
// Wrapped in runWithRecover so a panic in one tick does not kill the loop.
func (s *Server) runChildSessionWatcher(ctx context.Context) {
	// Run once immediately on startup to catch any sessions that
	// completed while ocman was down.
	runWithRecover("child-session-watcher", func() { childSessionWatchTickFn(s) })

	ticker := time.NewTicker(childSessionWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("child-session-watcher", func() { childSessionWatchTickFn(s) })
		}
	}
}

// checkAndInjectChildResults is the per-tick body of the watcher. It:
//  1. Queries state.db for non-terminal child sessions.
//  2. For each, infers the current status via the platform adapter.
//  3. If the status has changed to a terminal state, updates state.db
//     and injects a result message into the parent session.
func (s *Server) checkAndInjectChildResults(ctx context.Context) {
	if s.stateDB == nil {
		return
	}

	children, err := s.stateDB.ListNonTerminalChildSessions()
	if err != nil {
		log.WithError(err).Warn("mcp-watcher: listing non-terminal child sessions")
		return
	}
	if len(children) == 0 {
		return
	}

	for _, cs := range children {
		s.processChildSession(ctx, cs)
	}
}

// processChildSession checks one child session and injects a result if
// it has reached a terminal state.
func (s *Server) processChildSession(ctx context.Context, cs state.ChildSession) {
	// Infer the current status via the platform adapter.
	newStatus, summary := s.inferChildStatus(ctx, cs)
	if newStatus == "" || newStatus == cs.Status {
		return
	}

	// Update state.db.
	var completedAt int64
	if isTerminalStatus(newStatus) {
		completedAt = time.Now().UnixMilli()
	}
	if err := s.stateDB.UpdateChildSession(cs.ID, newStatus, summary, completedAt); err != nil {
		log.WithFields(log.Fields{
			"childSessionID": cs.ID,
			"newStatus":      newStatus,
			"error":          err,
		}).Warn("mcp-watcher: updating child session status")
	}

	log.WithFields(log.Fields{
		"childSessionID":  cs.ID,
		"parentSessionID": cs.ParentSessionID,
		"oldStatus":       cs.Status,
		"newStatus":       newStatus,
	}).Info("mcp-watcher: child session status changed")

	// Inject a result message into the parent session when terminal.
	if isTerminalStatus(newStatus) {
		// Loop-attached children route their completion to the compatibility
		// workflow. Non-loop children keep the one-shot injection.
		if cs.LoopID != "" {
			s.routeChildCompletionToLoop(ctx, cs)
			return
		}
		s.injectResultIntoParent(ctx, cs, newStatus, summary)
	}
}

// routeChildCompletionToLoop nudges the owning compatibility workflow now
// that one of its children has reached a terminal state. The workflow trigger
// engine would catch it on its next tick; this just shortens the latency.
func (s *Server) routeChildCompletionToLoop(ctx context.Context, cs state.ChildSession) {
	l, err := s.stateDB.GetLoop(cs.LoopID)
	if err != nil {
		log.WithFields(log.Fields{"loopID": cs.LoopID, "childID": cs.ID, "error": err}).
			Warn("mcp-watcher: loading loop for child completion")
		return
	}
	if l.State != "active" {
		return
	}
	if _, err := s.stateDB.GetLoopWorkflow(cs.LoopID); err != nil {
		log.WithFields(log.Fields{"loopID": cs.LoopID, "childID": cs.ID, "error": err}).
			Warn("mcp-watcher: loading loop workflow map")
		return
	}
	if err := s.workflowSvc().TriggerCompatibility(ctx, l.ID); err != nil {
		log.WithFields(log.Fields{"loopID": cs.LoopID, "error": err}).
			Warn("mcp-watcher: routing child completion to loop")
	}
}

// inferChildStatus looks up the child session via the platform adapter
// and returns the inferred status and a brief summary. Returns ("", "")
// if the session cannot be found or its status is unchanged.
func (s *Server) inferChildStatus(ctx context.Context, cs state.ChildSession) (status, summary string) {
	p, ok := s.registry.PlatformForSession(ctx, cs.ID)
	if !ok {
		// Session not found in any platform — may have been deleted.
		return "", ""
	}

	detail, err := p.Session(ctx, cs.ID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return "", ""
	}

	sess := detail.Session
	// Map OpenCode's session status to our child session status.
	//
	// OpenCode documents four statuses ("waiting", "busy", "done",
	// "error"; see db.Session.Status), but only "busy" means the LLM is
	// still working. Any other value — including an empty string or a
	// status this code doesn't recognise — means the turn is no longer
	// running, so we treat it as terminal. Previously the switch fell
	// through to ("", "") for unrecognised statuses, which left the
	// child session stuck in "starting"/"running" forever: the watcher
	// kept re-polling it every tick, never marked it terminal, and never
	// injected a result into the parent. That is the "prompt handled by
	// the LLM but the session never closes" bug.
	switch sess.Status {
	case "busy":
		return "running", ""
	case "error":
		return "error", sess.LastErrorMessage
	default:
		// "waiting", "done", "", and any unrecognised status: the LLM
		// turn has finished (or the session is no longer active), so
		// close the child session.
		return "completed", extractSummaryFromSession(*sess)
	}
}

// extractSummaryFromSession builds a brief summary from a completed session.
func extractSummaryFromSession(sess db.Session) string {
	if sess.Title != "" {
		return fmt.Sprintf("Session '%s' completed (%d messages)", sess.Title, sess.MessageCount)
	}
	return fmt.Sprintf("Session completed (%d messages)", sess.MessageCount)
}

// injectResultIntoParent sends a result notification to the parent session
// via Platform.SendMessage. Errors are logged but not propagated — the
// child session result is already stored in state.db.
func (s *Server) injectResultIntoParent(ctx context.Context, cs state.ChildSession, status, summary string) {
	p, ok := s.registry.PlatformForSession(ctx, cs.ParentSessionID)
	if !ok {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
		}).Warn("mcp-watcher: parent session platform not found; skipping injection")
		return
	}

	msg := buildInjectionMessage(cs, status, summary)

	if err := p.SendMessage(ctx, platforms.SendMessageRequest{
		SessionID: cs.ParentSessionID,
		Message:   msg,
	}); err != nil {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
			"error":           err,
		}).Warn("mcp-watcher: failed to inject result into parent session")
		return
	}

	log.WithFields(log.Fields{
		"parentSessionID": cs.ParentSessionID,
		"childSessionID":  cs.ID,
		"status":          status,
	}).Info("mcp-watcher: injected result into parent session")
}

// buildInjectionMessage composes the message injected into the parent session.
func buildInjectionMessage(cs state.ChildSession, status, summary string) string {
	var statusLine string
	switch status {
	case "completed":
		statusLine = "✅ Sub-task completed"
	case "error":
		statusLine = "❌ Sub-task failed"
	case "cancelled":
		statusLine = "🚫 Sub-task cancelled"
	default:
		statusLine = fmt.Sprintf("Sub-task %s", status)
	}

	msg := fmt.Sprintf("%s\n\n**Intent**: %s\n**Session ID**: `%s`", statusLine, cs.Intent, cs.ID)
	if cs.WorktreePath != "" {
		msg += fmt.Sprintf("\n**Worktree**: `%s`", cs.WorktreePath)
	}
	if summary != "" {
		msg += fmt.Sprintf("\n\n**Summary**: %s", summary)
	}
	return msg
}

// isTerminalStatus reports whether a status string is a terminal state.
// Duplicated from tools_status.go to avoid a cross-package dependency;
// both packages are small and the function is trivial.
func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "error", "cancelled":
		return true
	}
	return false
}
