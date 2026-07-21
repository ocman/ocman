package server

import (
	"context"
	"encoding/json"
	"fmt"
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
//  3. If the status has changed to a terminal state, updates state.db and
//     returns it to a waiting MCP call, or injects it into a detached parent.
func (s *Server) checkAndInjectChildResults(ctx context.Context) {
	if s.stateDB == nil {
		return
	}

	children, err := s.stateDB.ListPendingChildSessions()
	if err != nil {
		log.WithError(err).Warn("mcp-watcher: listing pending child sessions")
		return
	}
	if len(children) == 0 {
		return
	}

	for _, cs := range children {
		s.processChildSession(ctx, cs)
	}
}

// processChildSession checks one child session and delivers its terminal result.
func (s *Server) processChildSession(ctx context.Context, cs state.ChildSession) {
	if isTerminalStatus(cs.Status) {
		s.deliverChildResult(ctx, cs, cs.Status, cs.Summary)
		return
	}
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
		s.deliverChildResult(ctx, cs, newStatus, summary)
	}
}

func (s *Server) deliverChildResult(ctx context.Context, cs state.ChildSession, status, summary string) {
	if s.childResults != nil && s.childResults.Deliver(cs.ID, internalmcp.ChildResult{Status: status, Summary: summary}) {
		return
	}
	latest, err := s.stateDB.GetChildSession(cs.ID)
	if err != nil {
		return
	}
	if latest.ResultDelivery == "waiting" || latest.ResultDelivery == "disconnected" {
		_ = s.stateDB.SetChildResultDelivery(cs.ID, "disconnected")
		return
	}
	if latest.ResultDelivery != "detached" || !s.injectResultIntoParent(ctx, *latest, status, summary) {
		return
	}
	if err := s.stateDB.SetChildResultDelivery(cs.ID, "delivered"); err != nil {
		log.WithError(err).WithField("childSessionID", cs.ID).Warn("mcp-watcher: marking child result delivered")
		return
	}
	s.queueSvc().Flush(ctx, "", cs.ParentSessionID)
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
func (s *Server) injectResultIntoParent(ctx context.Context, cs state.ChildSession, status, summary string) bool {
	p, ok := s.registry.PlatformForSession(ctx, cs.ParentSessionID)
	if !ok {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
		}).Warn("mcp-watcher: parent session platform not found; skipping injection")
		return false
	}

	msg := buildInjectionMessage(cs, status, summary)

	queueID := fmt.Sprintf("child-result:%s:%d", cs.ID, cs.CompletedAt)
	if err := s.queueSvc().EnqueueOnce(ctx, queueID, string(p.ID()), platforms.SendMessageRequest{
		SessionID: cs.ParentSessionID,
		Message:   msg,
	}); err != nil {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
			"error":           err,
		}).Warn("mcp-watcher: failed to inject result into parent session")
		return false
	}

	log.WithFields(log.Fields{
		"parentSessionID": cs.ParentSessionID,
		"childSessionID":  cs.ID,
		"status":          status,
	}).Info("mcp-watcher: queued result for parent session")
	return true
}

// deferChildResultReconnect queues recovery guidance behind the parent's
// active turn. It never sends another prompt to the child.
func (s *Server) deferChildResultReconnect(childID string) {
	if s.stateDB == nil {
		return
	}
	cs, err := s.stateDB.GetChildSession(childID)
	if err != nil {
		log.WithError(err).WithField("childSessionID", childID).Warn("mcp: loading disconnected child session")
		return
	}
	message := fmt.Sprintf(
		"The result wait for child session %q disconnected. Resume the existing child without sending a new prompt by calling await_session_result with session_id %q and child_session_id %q. Do not call new_session again.",
		cs.ID, cs.ParentSessionID, cs.ID,
	)
	if err := s.queueSvc().Enqueue(context.Background(), cs.Platform, true, platforms.SendMessageRequest{
		SessionID: cs.ParentSessionID,
		Message:   message,
	}); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"childSessionID":  cs.ID,
			"parentSessionID": cs.ParentSessionID,
		}).Warn("mcp: queueing child result reconnect reminder")
	}
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
