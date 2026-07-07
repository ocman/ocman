package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/tmux"

	log "github.com/sirupsen/logrus"
)

func (s *Server) handleTmuxClients(w http.ResponseWriter, r *http.Request) {
	if !tmux.IsAvailable() {
		writeJSON(w, map[string]interface{}{
			"available": false,
			"clients":   []tmux.Client{},
		})
		return
	}

	clients, err := tmux.ListClients()
	if err != nil {
		if tmux.IsServerNotRunningError(err) {
			writeJSON(w, map[string]interface{}{
				"available": true,
				"clients":   []tmux.Client{},
			})
			return
		}
		log.WithError(err).Error("listing tmux clients")
		writeJSON(w, map[string]interface{}{
			"available": false,
			"clients":   []tmux.Client{},
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"available": true,
		"clients":   clients,
	})
}

func (s *Server) handleTmuxSessions(w http.ResponseWriter, r *http.Request) {
	if !tmux.IsAvailable() {
		writeJSON(w, map[string]interface{}{
			"available": false,
			"sessions":  []tmux.Session{},
		})
		return
	}

	sessions, err := tmux.ListSessions()
	if err != nil {
		if tmux.IsServerNotRunningError(err) {
			writeJSON(w, map[string]interface{}{
				"available": true,
				"sessions":  []tmux.Session{},
			})
			return
		}
		log.WithError(err).Error("listing tmux sessions")
		writeJSON(w, map[string]interface{}{
			"available": false,
			"sessions":  []tmux.Session{},
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"available": true,
		"sessions":  sessions,
	})
}

func (s *Server) handleTmuxLaunchOpencode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
		RemoteID  string `json:"remoteId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if (req.RemoteID == "" || req.RemoteID == "local") && !tmux.IsAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	if req.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	// Basic path sanity — must be absolute to avoid ambiguity.
	if !filepath.IsAbs(req.Directory) {
		http.Error(w, "directory must be an absolute path", http.StatusBadRequest)
		return
	}

	target := "local"
	if req.RemoteID != "" && req.RemoteID != "local" {
		target = "remote"
	}
	log.WithFields(log.Fields{
		"directory": req.Directory,
		"target":    target,
		"remoteId":  req.RemoteID,
	}).Info("launching opencode in tmux")

	host := s.router().ForDir(req.Directory)
	if req.RemoteID != "" {
		host = s.router().ForRemote(req.RemoteID)
	}
	res, err := host.LaunchTmux(r.Context(), hostsvc.LaunchTmuxRequest{Directory: req.Directory})
	if err != nil {
		log.WithError(err).Error("failed to launch opencode in tmux")
		serverError(w, "launching opencode in tmux", err)
		return
	}

	writeJSON(w, map[string]string{"session": res.Session})
}

func (s *Server) handleTmuxSwitch(w http.ResponseWriter, r *http.Request) {
	// The tmux-availability guard lives in this outer wrapper rather than
	// in handleTmuxSwitchWith so unit tests (which call the *With variant
	// directly with a stub runner) can exercise the handler on CI runners
	// that have no tmux binary installed.
	if !tmux.IsAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	s.handleTmuxSwitchWith(w, r, tmux.DefaultSwitchRunner)
}

func (s *Server) handleTmuxSwitchWith(w http.ResponseWriter, r *http.Request, runner tmux.SwitchRunner) {
	var req struct {
		Client  string `json:"client"`
		Session string `json:"session"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Session == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}

	// Validate the session name to prevent command injection.
	if len(req.Session) > 512 || !tmux.ValidName.MatchString(req.Session) {
		http.Error(w, "invalid session name", http.StatusBadRequest)
		return
	}

	// Validate the target actually exists to avoid passing arbitrary
	// strings to tmux. Targets may be either a plain session name or a
	// `session:window` pair (used by worktree sessions, which open in a
	// named window inside the existing project session).
	existingSessions, err := runner.ListSessions()
	if err != nil {
		serverError(w, "verifying tmux session", err)
		return
	}
	sessionName := req.Session
	windowName := ""
	if idx := strings.Index(req.Session, ":"); idx >= 0 {
		sessionName = req.Session[:idx]
		windowName = req.Session[idx+1:]
	}
	sessionExists := false
	for _, ts := range existingSessions {
		if ts.Name == sessionName {
			sessionExists = true
			break
		}
	}
	if !sessionExists {
		http.Error(w, "tmux session not found", http.StatusNotFound)
		return
	}
	if windowName != "" {
		windows, err := runner.ListWindows(sessionName)
		if err != nil {
			serverError(w, "verifying tmux window", err)
			return
		}
		windowExists := false
		for _, w := range windows {
			if w.Name == windowName {
				windowExists = true
				break
			}
		}
		if !windowExists {
			http.Error(w, "tmux window not found", http.StatusNotFound)
			return
		}
	}

	// Determine the client TTY. The previous default of /dev/ttys000
	// was macOS-specific and broke on Linux (PTYs are /dev/pts/N), and
	// even on macOS would target the wrong terminal when more than one
	// was open. Instead, if no client is supplied, look at the live
	// list of connected tmux clients: if exactly one exists, use it;
	// otherwise the caller must disambiguate.
	existingClients, err := runner.ListClients()
	if err != nil {
		serverError(w, "verifying tmux client", err)
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

	// Validate the client TTY path.
	if !tmux.ValidTTYPath.MatchString(clientTTY) {
		http.Error(w, "invalid client TTY", http.StatusBadRequest)
		return
	}

	// Validate the client is actually a connected tmux client.
	clientExists := false
	for _, c := range existingClients {
		if c.TTY == clientTTY {
			clientExists = true
			break
		}
	}
	if !clientExists {
		http.Error(w, "tmux client not found", http.StatusNotFound)
		return
	}

	log.WithFields(log.Fields{
		"client":  clientTTY,
		"session": req.Session,
	}).Info("switching tmux client")

	if err := runner.SwitchClient(clientTTY, req.Session); err != nil {
		log.WithFields(log.Fields{
			"client":  clientTTY,
			"session": req.Session,
			"error":   err,
		}).Error("failed to switch tmux client")
		http.Error(w, "failed to switch tmux client", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
