package server

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

// tmuxClient represents a connected tmux client.
type tmuxClient struct {
	TTY     string `json:"tty"`
	Session string `json:"session"`
	Width   string `json:"width"`
	Height  string `json:"height"`
}

// tmuxSession represents a tmux session.
type tmuxSession struct {
	Name         string `json:"name"`
	ResolvedPath string `json:"resolvedPath"`
	Windows      int    `json:"windows"`
}

// resolveTmuxSessionPath expands a tmux session name (which may use ~ and
// underscores in place of dots) into an absolute filesystem path for matching
// against OpenCode session directories.
func resolveTmuxSessionPath(name string) string {
	p := name
	// Expand leading ~
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	// tmux replaces dots with underscores in session names.
	// Walk path segments: if the literal segment doesn't exist but a
	// version with dots does, prefer the dotted version.
	parts := strings.Split(p, "/")
	resolved := "/"
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		candidate := filepath.Join(resolved, seg)
		if _, err := os.Stat(candidate); err == nil {
			resolved = candidate
			continue
		}
		// Try replacing underscores with dots
		if strings.Contains(seg, "_") {
			dotted := strings.ReplaceAll(seg, "_", ".")
			dottedCandidate := filepath.Join(resolved, dotted)
			if _, err := os.Stat(dottedCandidate); err == nil {
				resolved = dottedCandidate
				continue
			}
		}
		// Fallback: keep the original segment
		resolved = candidate
	}
	return filepath.Clean(resolved)
}

// isTmuxAvailable checks whether the tmux binary is on PATH.
func isTmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// isLoopback returns true if the request originates from localhost.
func isLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	// Strip brackets from IPv6 addresses (e.g. "[::1]" -> "::1")
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host == "127.0.0.1" || host == "::1"
}

// requireLocalhost wraps a handler to reject non-loopback requests.
func requireLocalhost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// validTmuxName matches safe tmux session/client names.
// Allows alphanumeric, hyphens, underscores, dots, forward slashes,
// colons, and tildes (paths used as session names).
var validTmuxName = regexp.MustCompile(`^[a-zA-Z0-9._/~:-]+$`)

// validTTYPath matches /dev/ttysNNN or /dev/pts/N style TTY paths.
var validTTYPath = regexp.MustCompile(`^/dev/(ttys?\d+|pts/\d+)$`)

// listTmuxClients returns all connected tmux clients.
func listTmuxClients() ([]tmuxClient, error) {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}\t#{client_session}\t#{client_width}\t#{client_height}").Output()
	if err != nil {
		return nil, err
	}

	var clients []tmuxClient
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		clients = append(clients, tmuxClient{
			TTY:     parts[0],
			Session: parts[1],
			Width:   parts[2],
			Height:  parts[3],
		})
	}
	return clients, nil
}

// listTmuxSessions returns all tmux sessions.
func listTmuxSessions() ([]tmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_windows}").Output()
	if err != nil {
		return nil, err
	}

	var sessions []tmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		windows := 0
		for _, c := range parts[1] {
			if c >= '0' && c <= '9' {
				windows = windows*10 + int(c-'0')
			}
		}
		sessions = append(sessions, tmuxSession{
			Name:         parts[0],
			ResolvedPath: resolveTmuxSessionPath(parts[0]),
			Windows:      windows,
		})
	}
	return sessions, nil
}

// switchTmuxClient switches the given tmux client to the given session.
func switchTmuxClient(clientTTY, targetSession string) error {
	return exec.Command("tmux", "switch-client", "-c", clientTTY, "-t", targetSession).Run()
}

func (s *Server) handleTmuxClients(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		writeJSON(w, map[string]interface{}{
			"available": false,
			"clients":   []tmuxClient{},
		})
		return
	}

	clients, err := listTmuxClients()
	if err != nil {
		log.WithError(err).Error("listing tmux clients")
		writeJSON(w, map[string]interface{}{
			"available": false,
			"clients":   []tmuxClient{},
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"available": true,
		"clients":   clients,
	})
}

func (s *Server) handleTmuxSessions(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		writeJSON(w, map[string]interface{}{
			"available": false,
			"sessions":  []tmuxSession{},
		})
		return
	}

	sessions, err := listTmuxSessions()
	if err != nil {
		log.WithError(err).Error("listing tmux sessions")
		writeJSON(w, map[string]interface{}{
			"available": false,
			"sessions":  []tmuxSession{},
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		"available": true,
		"sessions":  sessions,
	})
}

func (s *Server) handleTmuxSwitch(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}

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
	if len(req.Session) > 512 || !validTmuxName.MatchString(req.Session) {
		http.Error(w, "invalid session name", http.StatusBadRequest)
		return
	}

	// Validate the session actually exists to avoid passing arbitrary
	// strings to tmux.
	existingSessions, err := listTmuxSessions()
	if err != nil {
		serverError(w, "verifying tmux session", err)
		return
	}
	sessionExists := false
	for _, ts := range existingSessions {
		if ts.Name == req.Session {
			sessionExists = true
			break
		}
	}
	if !sessionExists {
		http.Error(w, "tmux session not found", http.StatusNotFound)
		return
	}

	// Determine the client TTY.
	// If no client is specified, default to /dev/ttys000 for localhost.
	clientTTY := req.Client
	if clientTTY == "" {
		clientTTY = "/dev/ttys000"
	}

	// Validate the client TTY path.
	if !validTTYPath.MatchString(clientTTY) {
		http.Error(w, "invalid client TTY", http.StatusBadRequest)
		return
	}

	// Validate the client is actually a connected tmux client.
	existingClients, err := listTmuxClients()
	if err != nil {
		serverError(w, "verifying tmux client", err)
		return
	}
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

	if err := switchTmuxClient(clientTTY, req.Session); err != nil {
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
