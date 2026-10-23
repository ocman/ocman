// Package tmuxmod is the feature module for the /api/tmux controls: list
// the machine's tmux clients and sessions, switch an attached client, and
// launch a project's managed OpenCode instance.
//
// Process control stays in internal/tmux and owner routing in
// hostsvc.Host: sessions and launches go through the Host the server
// resolves, so a remote project's work runs on its owner. Only the client
// list and switch act on this machine's tmux directly, because a tmux
// client is a TTY attached here; switching a remote's client from this
// browser would attach nothing the user can see.
package tmuxmod

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/tmux"

	log "github.com/sirupsen/logrus"
)

// Deps is what the server supplies.
type Deps struct {
	// Local is the hub's own Host, the owner of directory-less routes.
	Local func() hostsvc.Host
	// ResolveOwner resolves an explicit or inferred owner. It writes the
	// response and returns false when an explicit remote is not connected.
	ResolveOwner func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool)
	Guards       module.Guards
	// Runner drives this machine's tmux clients; zero means the real tmux.
	Runner tmux.SwitchRunner
}

// Module owns the /api/tmux routes.
type Module struct{ deps Deps }

func NewModule(deps Deps) *Module {
	if deps.Runner.ListClients == nil {
		deps.Runner = tmux.DefaultSwitchRunner
	}
	return &Module{deps: deps}
}

func (m *Module) Name() string { return "tmux" }

// Routes uses method patterns, so the mux answers a wrong method with 405
// without probing the peer.
func (m *Module) Routes(r module.Router) {
	local := m.deps.Guards.Localhost
	r.Handle("GET /api/tmux/clients", local(m.handleClients))
	r.Handle("GET /api/tmux/sessions", local(m.handleSessions))
	r.Handle("POST /api/tmux/switch", local(m.handleSwitch))
	r.Handle("POST /api/tmux/launch-opencode", local(m.handleLaunchOpencode))
}

func (m *Module) handleClients(w http.ResponseWriter, r *http.Request) {
	if !tmux.IsAvailable() {
		httpx.WriteJSON(w, map[string]any{"available": false, "clients": []tmux.Client{}})
		return
	}
	clients, err := m.deps.Runner.ListClients(r.Context())
	if err != nil {
		if tmux.IsServerNotRunningError(err) {
			httpx.WriteJSON(w, map[string]any{"available": true, "clients": []tmux.Client{}})
			return
		}
		log.WithError(err).Error("listing tmux clients")
		httpx.WriteJSON(w, map[string]any{"available": false, "clients": []tmux.Client{}})
		return
	}
	httpx.WriteJSON(w, map[string]any{"available": true, "clients": clients})
}

// sessionRow is the wire shape of one /api/tmux/sessions row; the
// frontend keys off resolvedPath.
type sessionRow struct {
	Name         string `json:"name"`
	ResolvedPath string `json:"resolvedPath"`
}

func (m *Module) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !tmux.IsAvailable() {
		httpx.WriteJSON(w, map[string]any{"available": false, "sessions": []sessionRow{}})
		return
	}
	// The route is directory-less, so the hub's Host is the owner.
	sessions, err := m.deps.Local().TmuxSessions(r.Context())
	if err != nil {
		if tmux.IsServerNotRunningError(err) {
			httpx.WriteJSON(w, map[string]any{"available": true, "sessions": []sessionRow{}})
			return
		}
		log.WithError(err).Error("listing tmux sessions")
		httpx.WriteJSON(w, map[string]any{"available": false, "sessions": []sessionRow{}})
		return
	}
	rows := make([]sessionRow, 0, len(sessions))
	for _, ts := range sessions {
		rows = append(rows, sessionRow{Name: ts.Name, ResolvedPath: ts.Path})
	}
	httpx.WriteJSON(w, map[string]any{"available": true, "sessions": rows})
}

func (m *Module) handleLaunchOpencode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
		RemoteID  string `json:"remoteId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	isLocal := req.RemoteID == "" || req.RemoteID == "local"
	if isLocal && !tmux.IsAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	if req.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.Directory) {
		http.Error(w, "directory must be an absolute path", http.StatusBadRequest)
		return
	}
	target := "local"
	if !isLocal {
		target = "remote"
	}
	log.WithFields(log.Fields{
		"directory": req.Directory,
		"target":    target,
		"remoteId":  req.RemoteID,
	}).Info("launching opencode in tmux")

	host, ok := m.deps.ResolveOwner(w, req.Directory, req.RemoteID)
	if !ok {
		return
	}
	// EnsureProjectOpencode, not the raw tmux launcher: the manual launch
	// shares the singleflight guard and managed registry with the
	// automatic paths, so it cannot start a competing instance (#376 AC-3).
	res, err := host.EnsureRuntime(r.Context(), string(platforms.OpenCodeID), req.Directory)
	if err != nil {
		log.WithError(err).Error("failed to launch opencode in tmux")
		httpx.ServerError(w, "launching opencode in tmux", err)
		return
	}
	// The native runtime's ID is the tmux session name; "session" is the
	// frontend's wire name for it.
	httpx.WriteJSON(w, map[string]string{"session": res.Runtime.ID})
}

func (m *Module) handleSwitch(w http.ResponseWriter, r *http.Request) {
	if !tmux.IsAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	m.switchClient(w, r)
}

// switchClient is handleSwitch past the availability check, so tests run
// it with a fake Runner on machines without tmux.
func (m *Module) switchClient(w http.ResponseWriter, r *http.Request) {
	runner := m.deps.Runner
	var req struct {
		Client  string `json:"client"`
		Session string `json:"session"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.Session == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	// Validated before it reaches tmux to prevent command injection.
	if len(req.Session) > 512 || !tmux.ValidName.MatchString(req.Session) {
		http.Error(w, "invalid session name", http.StatusBadRequest)
		return
	}

	// The target must exist: a plain session or a `session:window` pair
	// (worktree sessions open in a named window of the project session).
	existingSessions, err := runner.ListSessions(r.Context())
	if err != nil {
		httpx.ServerError(w, "verifying tmux session", err)
		return
	}
	sessionName, windowName, _ := strings.Cut(req.Session, ":")
	if !slices.ContainsFunc(existingSessions, func(s tmux.Session) bool { return s.Name == sessionName }) {
		http.Error(w, "tmux session not found", http.StatusNotFound)
		return
	}
	if windowName != "" {
		windows, err := runner.ListWindows(r.Context(), sessionName)
		if err != nil {
			httpx.ServerError(w, "verifying tmux window", err)
			return
		}
		if !slices.ContainsFunc(windows, func(w tmux.Window) bool { return w.Name == windowName }) {
			http.Error(w, "tmux window not found", http.StatusNotFound)
			return
		}
	}

	// Without an explicit client, infer it only when exactly one is
	// attached; a default TTY would target the wrong terminal (#25).
	existingClients, err := runner.ListClients(r.Context())
	if err != nil {
		httpx.ServerError(w, "verifying tmux client", err)
		return
	}
	clientTTY := req.Client
	if clientTTY == "" {
		switch len(existingClients) {
		case 1:
			clientTTY = existingClients[0].TTY
		case 0:
			http.Error(w, "no tmux clients connected; cannot infer target", http.StatusBadRequest)
			return
		default:
			http.Error(w, "multiple tmux clients connected; specify which one", http.StatusBadRequest)
			return
		}
	}
	if !tmux.ValidTTYPath.MatchString(clientTTY) {
		http.Error(w, "invalid client TTY", http.StatusBadRequest)
		return
	}
	if !slices.ContainsFunc(existingClients, func(c tmux.Client) bool { return c.TTY == clientTTY }) {
		http.Error(w, "tmux client not found", http.StatusNotFound)
		return
	}

	fields := log.Fields{"client": clientTTY, "session": req.Session}
	log.WithFields(fields).Info("switching tmux client")
	if err := runner.SwitchClient(r.Context(), clientTTY, req.Session); err != nil {
		log.WithFields(fields).WithError(err).Error("failed to switch tmux client")
		http.Error(w, "failed to switch tmux client", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
