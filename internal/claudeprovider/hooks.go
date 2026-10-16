package claudeprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// HookEvents are the Claude hooks the ocman plugin registers (AD-11).
var HookEvents = []string{"SessionStart", "SessionEnd", "Stop", "Notification", "PreToolUse", "PostToolUse"}

// hookBusyWindow is how long a hook's busy report stands: no hook marks a
// killed claude, so a stale report falls back to the transcript.
const hookBusyWindow = 5 * time.Minute

// hookState is a terminal session's status as last reported by a hook.
type hookState struct {
	status db.SessionStatus
	at     int64 // Unix ms
}

func hookStatus(event string) (db.SessionStatus, bool) {
	switch event {
	case "PreToolUse", "PostToolUse":
		return db.StatusBusy, true
	case "Notification": // a permission prompt or an idle reminder: the user is needed
		return db.StatusWaiting, true
	case "SessionStart", "SessionEnd", "Stop":
		return db.StatusDone, true
	}
	return "", false
}

// hook updates the live view of a session started in a terminal (AD-10).
// Sessions ocman launched are ignored: their stream is authoritative, and
// --plugin-dir fires the same hooks for them.
func (l *live) hook(req providerprotocol.HookEvent) error {
	status, ok := hookStatus(req.Event)
	if !ok || req.SessionID == "" {
		return statusError{http.StatusBadRequest, fmt.Errorf("invalid hook event %q", req.Event)}
	}
	l.observeExternal(req)
	if req.Event == "SessionEnd" {
		l.mu.Lock()
		delete(l.hooks, req.SessionID)
		l.mu.Unlock()
		return nil // Process exit does not establish a successful turn outcome.
	}
	l.mu.Lock()
	l.syncOwnedLocked()
	_, mine := l.owned[req.SessionID]
	prev, seen := l.hooks[req.SessionID]
	l.mu.Unlock()
	if mine {
		return nil
	}
	apply := func() {
		l.mu.Lock()
		l.hooks[req.SessionID] = hookState{status: status, at: now()}
		l.mu.Unlock()
	}
	// Publish transitions only: every tool call fires two hooks, and each
	// published event is a synced spool write.
	if seen && prev.status == status {
		apply()
		return nil
	}
	event := providerprotocol.Event{Type: "session.idle"}
	props := map[string]any{"sessionID": req.SessionID}
	if status != db.StatusDone {
		event.Type, props["status"] = "session.status", map[string]any{"type": status}
	}
	event.Properties, _ = json.Marshal(props)
	return l.sup.Publish(event, apply)
}

// overlayHookLocked applies a hook-reported status to a terminal session.
// A busy report counts for hookBusyWindow: no hook marks a killed claude, so
// a stale busy falls back to the transcript. A settled report only wins
// over a transcript that has not been written since.
func (l *live) overlayHookLocked(s *db.Session) {
	h, ok := l.hooks[s.ID]
	if !ok || l.owned[s.ID] != nil {
		return
	}
	switch {
	case h.status == db.StatusBusy:
		if time.Since(time.UnixMilli(h.at)) < hookBusyWindow {
			s.Status = db.StatusBusy
		}
	case h.at >= s.TimeUpdated && (h.status == db.StatusWaiting || s.Status == db.StatusBusy || s.Status == db.StatusInterrupted):
		s.Status = h.status
	}
}
