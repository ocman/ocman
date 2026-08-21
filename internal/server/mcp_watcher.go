package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	// childSessionWatchInterval is how often the watcher polls for
	// child session completion.
	childSessionWatchInterval = 5 * time.Second

	// childOrphanPollLimit is how many consecutive polls may fail to
	// resolve a child (or its parent) before the row is reaped. A remote
	// mid-reconnect resolves again well within this; a deleted session
	// never will.
	childOrphanPollLimit = 12

	// childOrphanGrace is how long after creation a child is exempt from
	// reaping, so a session that has not yet appeared in the platform's
	// listing is never mistaken for a deleted one.
	childOrphanGrace = 2 * time.Minute
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
//     returns it to a waiting MCP call, or queues explicitly asynchronous feedback.
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
	if cs.ResultDelivery == state.ChildResultAsyncSending || cs.ResultDelivery == state.ChildResultWaitSending {
		if cs.Status == "sending" && s.childResults != nil && s.childResults.Registered(cs.ID) {
			return
		}
		delivery := state.ChildResultAsyncPending
		if cs.ResultDelivery == state.ChildResultWaitSending {
			delivery = "waiting"
		}
		_, _ = s.stateDB.CompleteChildFollowup(cs.ID, cs.ResultDelivery, delivery)
		return
	}
	if isTerminalStatus(cs.Status) {
		s.deliverChildResult(ctx, cs, cs.Status, cs.Summary)
		return
	}
	// Infer the current status via the platform adapter.
	newStatus, summary := s.inferChildStatus(ctx, cs)
	if newStatus == "" {
		// The session resolves on no platform: deleted in OpenCode, or
		// its owning remote was removed. Left alone the row stays
		// non-terminal forever, costs a registry fan-out every tick
		// (a gRPC round trip per remote), and strands any waiter.
		s.reapOrphanChild(ctx, cs, "child session is no longer available (deleted, or its machine was removed)")
		return
	}
	s.clearChildUnresolved(cs.ID)
	if newStatus == cs.Status {
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

// reapOrphanChild settles a child the watcher can no longer resolve, so
// the row leaves ListPendingChildSessions and any waiter is released.
// Only after childOrphanPollLimit consecutive failures, and never within
// childOrphanGrace of creation, so a brief remote outage or a session
// that has not yet surfaced is not mistaken for a deleted one.
func (s *Server) reapOrphanChild(ctx context.Context, cs state.ChildSession, reason string) {
	if time.Since(time.UnixMilli(cs.CreatedAt)) < childOrphanGrace {
		return
	}
	if s.recordChildUnresolved(cs.ID) < childOrphanPollLimit {
		return
	}
	s.clearChildUnresolved(cs.ID)
	log.WithFields(log.Fields{
		"childSessionID":  cs.ID,
		"parentSessionID": cs.ParentSessionID,
		"reason":          reason,
	}).Warn("mcp-watcher: reaping unresolvable child session")

	if err := s.stateDB.UpdateChildSession(cs.ID, "error", reason, time.Now().UnixMilli()); err != nil {
		log.WithError(err).WithField("childSessionID", cs.ID).Warn("mcp-watcher: reaping child session")
		return
	}
	cs.Status = "error"
	cs.Summary = reason
	s.deliverChildResult(ctx, cs, "error", reason)
}

// recordChildUnresolved counts a consecutive failed resolution and
// returns the running total.
func (s *Server) recordChildUnresolved(childID string) int {
	s.childUnresolvedMu.Lock()
	defer s.childUnresolvedMu.Unlock()
	if s.childUnresolved == nil {
		s.childUnresolved = map[string]int{}
	}
	s.childUnresolved[childID]++
	return s.childUnresolved[childID]
}

func (s *Server) clearChildUnresolved(childID string) {
	s.childUnresolvedMu.Lock()
	delete(s.childUnresolved, childID)
	s.childUnresolvedMu.Unlock()
}

func (s *Server) deliverChildResult(ctx context.Context, cs state.ChildSession, status, summary string) {
	latest, err := s.stateDB.GetChildSession(cs.ID)
	if err != nil {
		return
	}
	switch latest.ResultDelivery {
	case "waiting":
		if s.childResults != nil && s.childResults.Deliver(cs.ID, internalmcp.ChildResult{Status: status, Summary: summary}) {
			return
		}
		// No in-process waiter: ocman restarted while the parent was
		// blocked on new_session. awaitChildResult tells the parent to
		// reconnect when IT observes the disconnect, but after a restart
		// the broker is empty and nobody does — so the parent hears
		// nothing and typically re-runs new_session, duplicating the
		// child's work. Enqueue the same reminder here.
		disconnected, err := s.stateDB.CompareAndSetChildResultDelivery(cs.ID, "waiting", "disconnected")
		if err == nil && disconnected {
			s.deferChildResultReconnect(cs.ID)
		}
		return
	case state.ChildResultAsyncPending:
		claimed, claimErr := s.stateDB.CompareAndSetChildResultDelivery(cs.ID, state.ChildResultAsyncPending, state.ChildResultAsyncQueueing)
		if claimErr != nil {
			log.WithError(claimErr).WithField("childSessionID", cs.ID).Warn("mcp-watcher: claiming async child result")
			return
		}
		if !claimed {
			return
		}
		latest.ResultDelivery = state.ChildResultAsyncQueueing
	case state.ChildResultAsyncQueueing:
	case "detached":
		if isTerminalStatus(cs.Status) {
			return
		}
		claimed, claimErr := s.stateDB.CompareAndSetChildResultDelivery(cs.ID, "detached", state.ChildResultAsyncQueueing)
		if claimErr != nil || !claimed {
			return
		}
		latest.ResultDelivery = state.ChildResultAsyncQueueing
	default:
		return
	}
	s.injectResultIntoParent(ctx, *latest, status, summary)
}

// ownerOf resolves the adapter that owns sessionID, preferring the
// platform persisted on the child row.
//
// The persisted platform is a preference, not a verdict. `child_sessions`
// has one `platform` column but a split can straddle two machines: a
// worktree split records the MCP server's own platform id while the
// owning host — possibly a remote — is the one that actually created the
// session (see mcp.splitTools.launchWorktree). Trusting the column
// blindly there resolves a remote-owned child to the local adapter, which
// cannot see it, and the child never settles.
//
// So: use the persisted platform only when it genuinely owns the session,
// otherwise fall back to the registry fan-out. That keeps the persisted
// identity authoritative for the case it exists to protect — two adapters
// holding the same bare session id, where the fan-out could pick either —
// without breaking cross-machine splits. Owns is cheap by contract (no
// HTTP, no subprocess), so the extra probe costs nothing.
func (s *Server) ownerOf(ctx context.Context, platform, sessionID string) (platforms.Platform, bool) {
	if platform != "" {
		if p, ok := s.registry.Get(platforms.ID(platform)); ok && p.Owns(ctx, sessionID) {
			return p, true
		}
	}
	return s.registry.PlatformForSession(ctx, sessionID)
}

// inferChildStatus looks up the child session via the platform adapter
// and returns the inferred status and a brief summary. Returns ("", "")
// if the session cannot be found or its status is unchanged.
func (s *Server) inferChildStatus(ctx context.Context, cs state.ChildSession) (status, summary string) {
	p, ok := s.ownerOf(ctx, cs.Platform, cs.ID)
	if !ok {
		// Session not found in any platform — may have been deleted.
		return "", ""
	}

	detail, err := p.Session(ctx, cs.ID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return "", ""
	}

	sess := detail.Session
	// Map the session status (see db.SessionStatus) to our child session
	// status. Only "busy" means the LLM is still working. Any other value
	// — including an empty string or a status this code doesn't recognise
	// — means the turn is no longer running, so we treat it as terminal.
	// Previously the switch fell through to ("", "") for unrecognised
	// statuses, which left the child session stuck in
	// "starting"/"running" forever: the watcher kept re-polling it every
	// tick, never marked it terminal, and never injected a result into
	// the parent. That is the "prompt handled by the LLM but the session
	// never closes" bug.
	switch sess.Status {
	case db.StatusBusy:
		return "running", ""
	case db.StatusError:
		return "error", sess.LastErrorMessage
	case db.StatusInterrupted:
		// The process running the child died mid-turn. Report it as an
		// error so the parent learns the work was lost instead of
		// receiving a truncated turn as a finished result.
		return "error", "child session was interrupted before the turn finished"
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

// injectResultIntoParent durably holds the child's terminal turn for the
// parent's next real idle edge or queue sweep.
func (s *Server) injectResultIntoParent(ctx context.Context, cs state.ChildSession, status, summary string) {
	p, ok := s.ownerOf(ctx, cs.Platform, cs.ParentSessionID)
	if !ok {
		// No parent to deliver to. Retrying forever re-listed this row
		// every tick and logged a WARN each time; once the parent has
		// been unresolvable for long enough, drop the result.
		if s.recordChildUnresolved(cs.ID) >= childOrphanPollLimit {
			s.clearChildUnresolved(cs.ID)
			log.WithFields(log.Fields{
				"parentSessionID": cs.ParentSessionID,
				"childSessionID":  cs.ID,
			}).Warn("mcp-watcher: dropping child result; parent session is gone")
			_ = s.stateDB.SetChildResultDelivery(cs.ID, "delivered")
			return
		}
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
		}).Debug("mcp-watcher: parent session platform not found; skipping injection")
		return
	}
	s.clearChildUnresolved(cs.ID)

	// The parent was archived while the child was working. Injecting the
	// result would advance the parent's time_updated, auto-unarchive it,
	// and start a fresh turn in a session the user deliberately hid.
	// Drop the injection — the child's status and summary stay readable
	// via list_child_sessions and the UI — and settle the row so the
	// watcher stops revisiting it.
	if archived, err := s.stateDB.IsSessionArchived(string(p.ID()), cs.ParentSessionID); err != nil {
		log.WithError(err).WithField("parentSessionID", cs.ParentSessionID).
			Warn("mcp-watcher: checking parent archive state")
	} else if archived {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
		}).Info("mcp-watcher: parent session is archived; dropping child result")
		_ = s.stateDB.SetChildResultDelivery(cs.ID, "delivered")
		return
	}

	msg := buildInjectionMessage(cs, status, summary)

	queueID := fmt.Sprintf("child-result:%s:%d", cs.ID, cs.CompletedAt)
	queued, err := s.queueSvc().EnqueueChildResult(ctx, cs.ID, queueID, string(p.ID()), platforms.SendMessageRequest{
		SessionID: cs.ParentSessionID,
		Message:   msg,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"parentSessionID": cs.ParentSessionID,
			"childSessionID":  cs.ID,
			"error":           err,
		}).Warn("mcp-watcher: failed to inject result into parent session")
		return
	}
	if !queued {
		return
	}

	log.WithFields(log.Fields{
		"parentSessionID": cs.ParentSessionID,
		"childSessionID":  cs.ID,
		"status":          status,
	}).Info("mcp-watcher: queued result for parent session")
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
