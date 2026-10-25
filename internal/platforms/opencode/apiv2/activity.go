package ocv2

import (
	"context"
	"net/http"
	"path/filepath"
)

// activity tracks foreground execution separately from background shells and
// native child agents. Only foreground execution determines session status.
// Shell metadata and native parent links establish background-task ownership.
// Each stream owns its snapshot, so reconnects discard stale process state.
type activity struct {
	foreground map[string]any
	shells     map[string]string // shell ID -> session ID
	commands   map[string]string
	parents    map[string]string
	titles     map[string]string
}

func (a *activity) backgroundCounts() map[string]int {
	counts := map[string]int{}
	for _, owner := range a.shells {
		counts[owner]++
	}
	for owner, agents := range a.agentDetails() {
		counts[owner] += len(agents)
	}
	return counts
}

func (t *translator) emitBackgroundCount(sessionID, dir string) error {
	return t.emit(sessionID, dir, "ocman.session.background", map[string]any{
		"count": t.activity.backgroundCounts()[sessionID],
		"tasks": t.activity.backgroundDetails()[sessionID],
	})
}

func (a *activity) busy(sessionID string) bool {
	_, ok := a.foreground[sessionID]
	return ok
}

func (a *activity) addShell(info map[string]any) string {
	id, owner := str(info, "id"), str(obj(info, "metadata"), "sessionID")
	if id == "" || owner == "" || str(info, "status") != "running" {
		return ""
	}
	a.shells[id] = owner
	if a.commands == nil {
		a.commands = map[string]string{}
	}
	a.commands[id] = str(info, "command")
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
	for id := range a.activeSessions() {
		if err := a.loadParents(ctx, c, id); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func getStatus(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var foreground data[map[string]any]
	if err := c.call(r.Context(), http.MethodGet, "/api/session/active", nil, nil, &foreground); err != nil {
		return finish(r, 0, nil, err)
	}
	out := map[string]any{}
	for id := range foreground.Data {
		out[id] = map[string]any{"type": "busy"}
	}
	return reply(r, http.StatusOK, out), nil
}

// emitActivity follows foreground execution only. Background tasks have their
// own lifecycle and may remain available after attention and queue dispatch.
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
