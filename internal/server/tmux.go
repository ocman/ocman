package server

import (
	"fmt"
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

// tmuxSessionNameForPath derives a tmux session name from a directory path.
//
// To match the convention used by existing sessions (e.g.
// "~/src/github.com/NoUseFreak/ocman"), directories under the user's home
// are rendered as a tilde-relative path; directories outside home stay
// absolute. tmux itself replaces dots with underscores when displaying
// the name, so callers see e.g. "~/src/github_com/NoUseFreak/ocman".
//
// Empty/"."/"/" inputs fall back to "opencode" so we never hand tmux an
// invalid name.
func tmuxSessionNameForPath(directory string) string {
	if directory == "" || directory == "." || directory == "/" {
		return "opencode"
	}
	clean := filepath.Clean(directory)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		homeClean := filepath.Clean(home)
		if clean == homeClean {
			return "~"
		}
		if rel, err := filepath.Rel(homeClean, clean); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return clean
}

// launchOpencodeInTmux finds or creates a tmux session named after the given
// directory, opens a new window in it, and runs `opencode --port 0` there.
// It returns the name of the tmux session that was used/created.
func launchOpencodeInTmux(directory string) (string, error) {
	sessionName := tmuxSessionNameForPath(directory)

	// Check whether this tmux session already exists.
	sessionExists := false
	existing, err := listTmuxSessions()
	if err == nil {
		for _, ts := range existing {
			if ts.Name == sessionName {
				sessionExists = true
				break
			}
		}
	}

	if !sessionExists {
		// Create a detached tmux session rooted at directory.
		if err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-c", directory).Run(); err != nil {
			return "", fmt.Errorf("tmux new-session: %w", err)
		}
	} else {
		// Session exists — open a new window in it, rooted at directory.
		if err := exec.Command("tmux", "new-window", "-t", sessionName, "-c", directory).Run(); err != nil {
			return "", fmt.Errorf("tmux new-window: %w", err)
		}
	}

	// Send the opencode command to the current pane of the session.
	// -t targets the most-recently-created window (the one we just made).
	if err := exec.Command("tmux", "send-keys", "-t", sessionName, "opencode --port 0", "Enter").Run(); err != nil {
		return sessionName, fmt.Errorf("tmux send-keys: %w", err)
	}

	return sessionName, nil
}

func (s *Server) handleTmuxLaunchOpencode(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Directory string `json:"directory"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
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

	log.WithField("directory", req.Directory).Info("launching opencode in tmux")

	sessionName, err := launchOpencodeInTmux(req.Directory)
	if err != nil {
		log.WithError(err).Error("failed to launch opencode in tmux")
		serverError(w, "launching opencode in tmux", err)
		return
	}

	writeJSON(w, map[string]string{"session": sessionName})
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
