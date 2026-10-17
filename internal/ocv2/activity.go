package ocv2

import (
	"context"
	"net/http"
	"path/filepath"
)

// activity combines foreground execution with session-owned shell commands.
// V2's /session/active intentionally excludes background commands. Shell
// metadata is the ownership link; unowned terminals never hold a session busy.
// Each stream owns its snapshot, so reconnects discard stale process state.
type activity struct {
	foreground map[string]any
	shells     map[string]string // shell ID -> session ID
}

func (a *activity) backgroundCounts() map[string]int {
	counts := map[string]int{}
	for _, owner := range a.shells {
		counts[owner]++
	}
	return counts
}

func (t *translator) emitBackgroundCount(sessionID, dir string) error {
	return t.emit(sessionID, dir, "ocman.session.background", map[string]any{
		"count": t.activity.backgroundCounts()[sessionID],
	})
}

func (a *activity) busy(sessionID string) bool {
	if _, ok := a.foreground[sessionID]; ok {
		return true
	}
	for _, owner := range a.shells {
		if owner == sessionID {
			return true
		}
	}
	return false
}

func (a *activity) addShell(info map[string]any) string {
	id, owner := str(info, "id"), str(obj(info, "metadata"), "sessionID")
	if id == "" || owner == "" || str(info, "status") != "running" {
		return ""
	}
	a.shells[id] = owner
	return owner
}

func (c *compat) activity(ctx context.Context, directory string) (*activity, error) {
	var foreground data[map[string]any]
	if err := c.call(ctx, http.MethodGet, "/api/session/active", nil, nil, &foreground); err != nil {
		return nil, err
	}
	a := &activity{foreground: foreground.Data, shells: map[string]string{}}
	if a.foreground == nil {
		a.foreground = map[string]any{}
	}
	// Only inspect already-loaded locations. Asking for historical session
	// directories would activate plugins and retain new location instances.
	var locations []map[string]any
	if err := c.call(ctx, http.MethodGet, "/api/debug/location", nil, nil, &locations); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, loc := range locations {
		dir := str(loc, "directory")
		if dir == "" || seen[dir] {
			continue
		}
		if directory != "" && filepath.Clean(dir) != filepath.Clean(directory) {
			continue
		}
		seen[dir] = true
		var shells data[[]map[string]any]
		if err := c.call(ctx, http.MethodGet, "/api/shell", location(dir), nil, &shells); err != nil {
			return nil, err
		}
		for _, shell := range shells.Data {
			a.addShell(shell)
		}
	}
	return a, nil
}

func getStatus(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	a, err := c.activity(r.Context(), requestDirectory(r))
	if err != nil {
		return finish(r, 0, nil, err)
	}
	out := map[string]any{}
	for id := range a.foreground {
		out[id] = map[string]any{"type": "busy"}
	}
	for _, id := range a.shells {
		out[id] = map[string]any{"type": "busy"}
	}
	return reply(r, http.StatusOK, out), nil
}

// emitActivity publishes idle only when all foreground and background work
// has ended. Consumers use the same edge for attention and queue dispatch.
func (t *translator) emitActivity(sessionID, dir string) error {
	status := map[string]any{"type": "idle"}
	if t.activity.busy(sessionID) {
		status["type"] = "busy"
	}
	if retry, ok := t.activity.foreground[sessionID].(map[string]any); ok && str(retry, "type") == "retry" {
		status = retry
	}
	if err := t.emit(sessionID, dir, "session.status", map[string]any{"status": status}); err != nil {
		return err
	}
	if str(status, "type") == "idle" {
		return t.emit(sessionID, dir, "session.idle", map[string]any{})
	}
	return nil
}
