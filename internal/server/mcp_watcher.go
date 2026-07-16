package server

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
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

	// Queue a result message for the parent session when terminal.
	if isTerminalStatus(newStatus) {
		s.injectResultIntoParent(ctx, cs, newStatus, summary)
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
		if len(detail.Messages) > 0 {
			var data struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(detail.Messages[len(detail.Messages)-1].Data, &data) == nil && data.Role == "user" {
				return "running", ""
			}
		}
		// "waiting", "done", "", and any unrecognised status: the LLM
		// turn has finished (or the session is no longer active), so
		// close the child session.
		return "completed", finalAssistantMessage(detail.Messages, detail.Parts)
	}
}

// injectResultIntoParent queues the child's terminal turn for its parent.
// The queue sends it immediately when the parent is idle, or at its next idle
// edge when it is busy.
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

	if err := s.queueSvc().Enqueue(ctx, string(p.ID()), false, platforms.SendMessageRequest{
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
	}).Info("mcp-watcher: queued result for parent session")
}

// buildInjectionMessage composes the message injected into the parent session.
func buildInjectionMessage(cs state.ChildSession, status, summary string) string {
	kind := "completion"
	switch status {
	case "error":
		kind = "error"
	case "cancelled":
		kind = "cancellation"
	}
	return internalmcp.FormatUntrustedChildMessage(kind, cs.ID, cs.Intent, status, summary)
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
