package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// ocmanTermSession is the single dedicated tmux session that hosts every
// in-app terminal window. All terminals — across every project and
// worktree — live as named windows inside this one session, so ocman
// never scatters windows into the user's project sessions and never
// leaks per-viewer grouped sessions (the previous model created an
// ephemeral `ocman-view-<uuid>` session per WebSocket, which leaked on
// restart). Window names encode which directory each terminal belongs
// to; see termWindowName.
const ocmanTermSession = "ocman-term"

// termUpgrader upgrades /api/term/ws to a WebSocket. The endpoint is
// localhost-only (gated by requireLocalhost in the route table), so the
// CheckOrigin allowlist is intentionally narrow: same-origin loopback
// only. Combined with requireLocalhost this keeps the PTY bridge
// unreachable from other origins or the network.
var termUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return isLoopback(r)
	},
}

// termResize is the only control message the client sends as text JSON.
// All other client->server frames are raw keystrokes forwarded to the
// PTY verbatim.
type termResize struct {
	Type string `json:"type"` // "resize"
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// handleTermWS attaches a browser xterm.js terminal to a dedicated
// terminal window in the single `ocman` tmux session.
//
// All terminal windows live in one session (ocmanTermSession). Each
// WebSocket attaches a PTY directly to that session and selects the
// requested window. The session is configured with `window-size
// manual` so each window can be sized independently per viewer via
// `resize-window`, driven from the browser's reported xterm dimensions
// — so one small browser tab can't shrink another tab's window.
//
// Query params:
//   - dir:      the OpenCode session's working directory (required).
//   - window:   a specific window name to attach to (optional; when
//     omitted the first window for dir is reused, or one is created).
//   - readonly: "1" to attach read-only (tmux attach -r).
//
// Mounted with requireLocalhost; this is a live shell, do not expose it
// on a non-loopback bind without rethinking auth.
func (s *Server) handleTermWS(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}

	dir := r.URL.Query().Get("dir")
	if dir == "" || !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	windowName := r.URL.Query().Get("window")
	if windowName != "" {
		// Explicit window must be a well-formed terminal window for dir,
		// so a caller can't target arbitrary windows.
		if !isTermWindowForDir(windowName, dir) {
			http.Error(w, "invalid window", http.StatusBadRequest)
			return
		}
		if err := ensureOcmanSession(); err != nil {
			serverError(w, "ensuring terminal session", err)
			return
		}
		if !termWindowExists(windowName) {
			http.Error(w, "terminal window not found", http.StatusNotFound)
			return
		}
	} else {
		// No explicit window: reuse the first window for dir, or create
		// one. ensureTermWindow also ensures the ocman session exists.
		win, err := ensureTermWindow(dir)
		if err != nil {
			log.WithError(err).WithField("dir", dir).
				Error("ensuring dedicated terminal window")
			http.Error(w, "could not create terminal window", http.StatusInternalServerError)
			return
		}
		windowName = win
	}

	readonly := r.URL.Query().Get("readonly") == "1"

	conn, err := termUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an error response.
		log.WithError(err).Debug("term ws upgrade failed")
		return
	}
	defer conn.Close()

	// Attach a PTY directly to the ocman session, selecting the target
	// window. `-f ignore-size` would pin the client size; instead we
	// rely on the session's `window-size manual` (set in
	// ensureOcmanSession) plus per-window resize-window calls below so
	// each viewer sizes its own window independently.
	target := ocmanTermSession + ":" + windowName
	args := []string{"attach-session", "-t", target}
	if readonly {
		args = append(args, "-r")
	}
	cmd := exec.Command("tmux", args...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.WithError(err).Error("starting pty for tmux attach")
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "pty start failed"))
		return
	}
	defer func() { _ = ptmx.Close() }()

	// Tear down the tmux client process and PTY when the socket closes
	// from either side. Detaching the client does NOT kill the window,
	// so the shell and its scrollback survive reconnects.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// PTY -> WebSocket: server output to the browser, sent as binary.
	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// WebSocket -> PTY: browser keystrokes + resize control frames.
	for {
		mt, data, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		switch mt {
		case websocket.TextMessage:
			// Control frame: only resize is understood. JSON with a
			// different type is ignored; non-JSON text is treated as
			// keystrokes.
			var rz termResize
			if json.Unmarshal(data, &rz) == nil && rz.Type == "resize" {
				if rz.Rows > 0 && rz.Cols > 0 {
					// Size the PTY (the tmux client) ...
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rz.Rows, Cols: rz.Cols})
					// ... and the specific window, so this viewer's size
					// doesn't depend on other clients attached to the
					// shared ocman session (window-size is manual).
					_ = exec.Command("tmux", "resize-window", "-t", target,
						"-x", strconv.Itoa(int(rz.Cols)), "-y", strconv.Itoa(int(rz.Rows))).Run()
				}
				continue
			}
			if !readonly {
				_, _ = ptmx.Write(data)
			}
		case websocket.BinaryMessage:
			if !readonly {
				_, _ = ptmx.Write(data)
			}
		}
	}
}

// ── REST: /api/term/windows ──────────────────────────────────────────

// termWindowsRequest is the shared shape for the terminal-window REST
// endpoints. `dir` is the OpenCode session directory the windows belong
// to; `window` (DELETE only) is the window to kill.
type termWindowsRequest struct {
	Dir    string `json:"dir"`
	Window string `json:"window"`
}

// handleTermWindows is the multi-method handler for /api/term/windows:
//
//	GET    ?dir=<dir>      -> list terminal windows for dir
//	POST   {dir}           -> create a new terminal window for dir
//	DELETE {dir,window}    -> kill a terminal window
//
// All variants are localhost-only (wired with requireLocalhost). Every
// window lives in the single `ocman` session and is named so only
// windows belonging to dir are listed or killed.
func (s *Server) handleTermWindows(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleTermWindowsList(w, r)
	case http.MethodPost:
		s.handleTermWindowsCreate(w, r)
	case http.MethodDelete:
		s.handleTermWindowsDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTermWindowsList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if !validDir(w, dir) {
		return
	}
	// No ocman session yet means no terminals; return an empty list so
	// the UI shows a clean "+" state.
	if !ocmanSessionExists() {
		writeJSON(w, map[string]any{"windows": []termWindow{}})
		return
	}
	windows, err := listTermWindowInfo(dir)
	if err != nil {
		serverError(w, "listing terminal windows", err)
		return
	}
	if windows == nil {
		windows = []termWindow{}
	}
	writeJSON(w, map[string]any{"windows": windows})
}

func (s *Server) handleTermWindowsCreate(w http.ResponseWriter, r *http.Request) {
	var req termWindowsRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validDir(w, req.Dir) {
		return
	}
	name, err := createTermWindow(req.Dir)
	if err != nil {
		serverError(w, "creating terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": name, "dir": req.Dir}).
		Info("created terminal window")
	writeJSON(w, map[string]any{"window": name})
}

func (s *Server) handleTermWindowsDelete(w http.ResponseWriter, r *http.Request) {
	var req termWindowsRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validDir(w, req.Dir) {
		return
	}
	// The window must belong to *this* dir's terminal set, so a caller
	// can't kill arbitrary windows.
	if !isTermWindowForDir(req.Window, req.Dir) || !termWindowExists(req.Window) {
		http.Error(w, "terminal window not found", http.StatusNotFound)
		return
	}
	if err := exec.Command("tmux", "kill-window", "-t",
		ocmanTermSession+":"+req.Window).Run(); err != nil {
		serverError(w, "killing terminal window", err)
		return
	}
	log.WithFields(log.Fields{"window": req.Window, "dir": req.Dir}).
		Info("killed terminal window")
	w.WriteHeader(http.StatusNoContent)
}

// validDir validates the dir param shared by the terminal-window
// endpoints, writing a 400 and returning false on failure.
func validDir(w http.ResponseWriter, dir string) bool {
	if dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return false
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return false
	}
	return true
}

// ── ocman session management ─────────────────────────────────────────

// ocmanSessionExists reports whether the dedicated terminal session is
// currently running.
func ocmanSessionExists() bool {
	return exec.Command("tmux", "has-session", "-t", ocmanTermSession).Run() == nil
}

// ensureOcmanSession creates the dedicated terminal session if it does
// not exist, configured for the single-session model:
//   - created detached, with one throwaway initial window we immediately
//     rename out of the terminal namespace so it's never surfaced.
//   - window-size manual: each window is sized explicitly per viewer via
//     resize-window, so multiple browser tabs attached to the shared
//     session don't fight over a single client size.
//   - status off: no tmux chrome leaks into the browser pane.
func ensureOcmanSession() error {
	if ocmanSessionExists() {
		return nil
	}
	// Create detached. The initial window is named "_ocman_placeholder"
	// so it never matches a terminal window pattern; it keeps the
	// session alive even when no terminals exist yet. (tmux requires at
	// least one window per session.)
	if err := exec.Command("tmux", "new-session", "-d",
		"-s", ocmanTermSession, "-n", "_ocman_placeholder").Run(); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	for _, opt := range [][]string{
		{"set-option", "-t", ocmanTermSession, "window-size", "manual"},
		{"set-option", "-t", ocmanTermSession, "status", "off"},
		{"set-option", "-t", ocmanTermSession, "pane-border-status", "off"},
		// Don't let the placeholder/idle window auto-rename and confuse
		// our name-based tracking.
		{"set-option", "-t", ocmanTermSession, "allow-rename", "off"},
		{"set-option", "-t", ocmanTermSession, "automatic-rename", "off"},
	} {
		if err := exec.Command("tmux", opt...).Run(); err != nil {
			log.WithError(err).WithField("option", opt[3]).
				Debug("setting ocman session option")
		}
	}
	return nil
}

// ── window naming & tracking ─────────────────────────────────────────
//
// Window names encode which directory a terminal belongs to:
//
//	ocman-<hash>-<n>
//
// where <hash> is a short stable hash of the absolute directory and <n>
// is a per-directory index. Hashing avoids tmux-illegal characters and
// length limits from raw paths, and avoids the basename collisions the
// old slug scheme had (two different repos both named "app"). The
// backend is the only thing that needs to map hash<->dir, and it does so
// by recomputing the hash for the dir in question and filtering.

// termWindowRe matches the dedicated terminal window naming scheme and
// captures the hash and numeric index.
var termWindowRe = regexp.MustCompile(`^ocman-([0-9a-f]{10})-(\d+)$`)

// dirHash returns a short stable hash of the cleaned absolute directory.
func dirHash(dir string) string {
	sum := sha1.Sum([]byte(filepath.Clean(dir)))
	return hex.EncodeToString(sum[:])[:10]
}

// termWindowPrefix is the `ocman-<hash>-` stem shared by every terminal
// window for a directory.
func termWindowPrefix(dir string) string {
	return "ocman-" + dirHash(dir) + "-"
}

// isTermWindowForDir reports whether name is a well-formed terminal
// window belonging to dir.
func isTermWindowForDir(name, dir string) bool {
	m := termWindowRe.FindStringSubmatch(name)
	return m != nil && m[1] == dirHash(dir)
}

// termWindowIndex extracts the trailing numeric index from a terminal
// window name, or 0 when it doesn't match.
func termWindowIndex(name string) int {
	m := termWindowRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// allTermWindowNames lists every terminal window in the ocman session
// (across all directories) that matches the naming scheme.
func allTermWindowNames() ([]string, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", ocmanTermSession,
		"-F", "#{window_name}").Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if termWindowRe.MatchString(line) {
			names = append(names, line)
		}
	}
	return names, nil
}

// termWindowExists reports whether a specific terminal window currently
// exists in the ocman session.
func termWindowExists(name string) bool {
	names, err := allTermWindowNames()
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// listTermWindowNames returns the terminal windows for dir in ascending
// index order.
func listTermWindowNames(dir string) ([]string, error) {
	all, err := allTermWindowNames()
	if err != nil {
		return nil, err
	}
	prefix := termWindowPrefix(dir)
	var names []string
	for _, n := range all {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return termWindowIndex(names[i]) < termWindowIndex(names[j])
	})
	return names, nil
}

// createTermWindow creates a new terminal window for dir in the ocman
// session, rooted at dir, with the lowest free index. Ensures the ocman
// session exists first. Returns the new window name.
//
// The window hosts an ordinary login shell so the user gets a real
// interactive prompt. It is persistent: detaching a viewer does not kill
// it, so shell state/history survive reconnects.
func createTermWindow(dir string) (string, error) {
	if err := ensureOcmanSession(); err != nil {
		return "", err
	}
	existing, err := listTermWindowNames(dir)
	if err != nil {
		return "", fmt.Errorf("listing terminal windows: %w", err)
	}
	used := make(map[int]bool, len(existing))
	for _, n := range existing {
		used[termWindowIndex(n)] = true
	}
	idx := 1
	for used[idx] {
		idx++
	}
	windowName := termWindowPrefix(dir) + strconv.Itoa(idx)
	if !validTmuxComponent.MatchString(windowName) {
		return "", fmt.Errorf("derived terminal window name %q is invalid", windowName)
	}
	// Create detached so we never steal focus; viewers attach + select
	// it themselves.
	if err := exec.Command("tmux", "new-window", "-d",
		"-t", ocmanTermSession, "-n", windowName, "-c", dir).Run(); err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	return windowName, nil
}

// ensureTermWindow returns an existing terminal window for dir, or
// creates the first one when none exist.
func ensureTermWindow(dir string) (string, error) {
	if err := ensureOcmanSession(); err != nil {
		return "", err
	}
	existing, err := listTermWindowNames(dir)
	if err != nil {
		return "", fmt.Errorf("listing terminal windows: %w", err)
	}
	if len(existing) > 0 {
		return existing[0], nil
	}
	return createTermWindow(dir)
}

// ── window titles ────────────────────────────────────────────────────

// termWindow is a dedicated terminal window with a display title derived
// from what's running in it.
type termWindow struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// idleShells are pane_current_command values that mean "nothing
// interesting is running" — an idle shell prompt. Used to decide when a
// command name is worth showing as a tab title.
var idleShells = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true,
}

// listTermWindowInfo returns the terminal windows for dir (ascending
// index order), each with a display title: the program-set pane title
// (OSC) when meaningful, else the running command when not an idle
// shell, else empty (the UI falls back to the tab number).
func listTermWindowInfo(dir string) ([]termWindow, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", ocmanTermSession,
		"-F", "#{window_name}\t#{pane_current_command}\t#{pane_title}").Output()
	if err != nil {
		return nil, err
	}
	prefix := termWindowPrefix(dir)
	var wins []termWindow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		name := parts[0]
		if !strings.HasPrefix(name, prefix) || !termWindowRe.MatchString(name) {
			continue
		}
		cmd, paneTitle := "", ""
		if len(parts) > 1 {
			cmd = parts[1]
		}
		if len(parts) > 2 {
			paneTitle = parts[2]
		}
		wins = append(wins, termWindow{Name: name, Title: termWindowTitle(cmd, paneTitle)})
	}
	sort.Slice(wins, func(i, j int) bool {
		return termWindowIndex(wins[i].Name) < termWindowIndex(wins[j].Name)
	})
	return wins, nil
}

// termWindowTitle picks a display title from the pane's running command
// and program-set pane title.
func termWindowTitle(cmd, paneTitle string) string {
	pt := strings.TrimSpace(paneTitle)
	if pt != "" && pt != cmd && !looksLikeHostname(pt) {
		return pt
	}
	if cmd != "" && !idleShells[cmd] {
		return cmd
	}
	return ""
}

// looksLikeHostname is a cheap heuristic to reject tmux's default
// pane_title (the local hostname) so it isn't shown as a tab title.
func looksLikeHostname(s string) bool {
	if strings.ContainsAny(s, " /\\:") {
		return false
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if s == host || strings.HasPrefix(host, s) || strings.HasPrefix(s, host) {
			return true
		}
		if short, _, ok := strings.Cut(host, "."); ok && s == short {
			return true
		}
	}
	return false
}

// sweepLegacyTermSessions removes orphaned ephemeral viewer sessions
// from the previous implementation (`ocman-view-<uuid>`). They could
// never belong to a live connection at startup, so killing them at boot
// self-heals leaks across restarts. Safe no-op when tmux isn't running.
func sweepLegacyTermSessions() {
	if !isTmuxAvailable() {
		return
	}
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(name, "ocman-view-") {
			if err := exec.Command("tmux", "kill-session", "-t", name).Run(); err != nil {
				log.WithError(err).WithField("session", name).
					Debug("sweeping legacy ocman-view session")
			}
		}
	}
}
