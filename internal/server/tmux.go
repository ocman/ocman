package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"

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

// tmuxWindow represents one window inside a tmux session.
type tmuxWindow struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Command      string `json:"command,omitempty"`
	StartCommand string `json:"startCommand,omitempty"`
}

var errNoManagedOpencodePane = errors.New("no tmux-managed opencode pane found for session directory")

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

// isTmuxServerNotRunningError reports whether err is the error tmux emits when no
// server is running yet (e.g. on a fresh machine before the first session
// is created). This is distinct from "tmux is not installed": the binary
// exists and works, there's just no server to list from. Callers that
// have already verified isTmuxAvailable() should treat this as "tmux is
// available with an empty session/client list" rather than as a failure.
func isTmuxServerNotRunningError(err error) bool {
	if err == nil {
		return false
	}
	msg := ""
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		msg = string(exitErr.Stderr)
	}
	if msg == "" {
		msg = err.Error()
	}
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "no such file or directory")
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
// colons, and tildes (paths used as session names). Colons are allowed
// because user-supplied target identifiers may take the form
// `session:window`.
var validTmuxName = regexp.MustCompile(`^[a-zA-Z0-9._/~:-]+$`)

// validTmuxComponent matches a single tmux session or window name
// (no `:` allowed — that's the target separator). Used to validate
// names *derived from filesystem paths* before they're handed to tmux,
// where an embedded `:` would split into session/window and either
// error or silently mis-target.
var validTmuxComponent = regexp.MustCompile(`^[a-zA-Z0-9._/~-]+$`)

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

// listTmuxWindows returns all windows for the given tmux session. The
// `Path` is the current pane's working directory, which is good enough
// for worktree-window matching because each worktree session runs a
// single opencode process rooted at that directory.
func listTmuxWindows(sessionName string) ([]tmuxWindow, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_name}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_start_command}").Output()
	if err != nil {
		return nil, err
	}
	var windows []tmuxWindow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 2 {
			continue
		}
		command := ""
		if len(parts) == 3 {
			command = parts[2]
		}
		startCommand := ""
		if len(parts) == 4 {
			command = parts[2]
			startCommand = parts[3]
		}
		windows = append(windows, tmuxWindow{Name: parts[0], Path: filepath.Clean(parts[1]), Command: command, StartCommand: startCommand})
	}
	return windows, nil
}

func tmuxWindowRunsOpencode(win tmuxWindow) bool {
	return win.Command == "opencode" || strings.Contains(win.StartCommand, "opencode")
}

// worktreeTmuxSession is the single shared tmux session that collects
// every worktree window across all projects, so worktree launches never
// disturb a project's own session.
const worktreeTmuxSession = "ocman-worktree"

// tmuxWindowNameForDirectory derives the named tmux window used for a
// worktree rooted at directory. We qualify it with the parent project so
// worktrees from different projects can share the single
// worktreeTmuxSession without window-name collisions: the worktree slug
// alone (e.g. "feature-x") is only unique within a project. The layout
// under <repo-parent>/.worktrees/<repo>/<slug>/ gives us both segments.
func tmuxWindowNameForDirectory(directory string) string {
	clean := filepath.Clean(directory)
	slug := filepath.Base(clean)
	if slug == "." || slug == "/" || slug == "" {
		return "wt"
	}
	repo := filepath.Base(filepath.Dir(clean))
	if repo == "." || repo == "/" || repo == "" || repo == "worktrees" {
		return "wt-" + slug
	}
	return "wt-" + repo + "-" + slug
}

// findTmuxSessionByPathIn returns the tmux session whose resolved path
// matches directory, using the actual session name tmux reports (which
// may have dots replaced with underscores). This is the reliable way to
// target an existing project session — deriving a fresh name from the
// path and handing it back to tmux can fail because tmux's displayed
// name is not always byte-for-byte what we originally requested.
func findTmuxSessionByPathIn(existing []tmuxSession, directory string) *tmuxSession {
	want := filepath.Clean(directory)
	for _, ts := range existing {
		if filepath.Clean(ts.ResolvedPath) == want {
			copy := ts
			return &copy
		}
	}
	return nil
}

// switchTmuxClient switches the given tmux client to the given session.
func switchTmuxClient(clientTTY, targetSession string) error {
	return exec.Command("tmux", "switch-client", "-c", clientTTY, "-t", targetSession).Run()
}

// tmuxSwitchRunner abstracts the side-effectful calls used by
// handleTmuxSwitch so that unit tests can inject fakes without
// requiring a real tmux binary.
type tmuxSwitchRunner struct {
	listSessions func() ([]tmuxSession, error)
	listClients  func() ([]tmuxClient, error)
	listWindows  func(sessionName string) ([]tmuxWindow, error)
	switchClient func(clientTTY, targetSession string) error
}

var defaultTmuxSwitchRunner = tmuxSwitchRunner{
	listSessions: listTmuxSessions,
	listClients:  listTmuxClients,
	listWindows:  listTmuxWindows,
	switchClient: switchTmuxClient,
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
		if isTmuxServerNotRunningError(err) {
			writeJSON(w, map[string]interface{}{
				"available": true,
				"clients":   []tmuxClient{},
			})
			return
		}
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
		if isTmuxServerNotRunningError(err) {
			writeJSON(w, map[string]interface{}{
				"available": true,
				"sessions":  []tmuxSession{},
			})
			return
		}
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

// tmuxRunner abstracts the tmux side-effects so unit tests can stub
// them. Production code uses defaultTmuxRunner; tests inject a fake.
//
// `command` arguments to newSession/newWindow/newNamedWindow are passed
// to tmux as the positional shell-command argument: tmux runs that
// command directly in the new pane *in place of the user's shell*. We
// use this for opencode launches because the user's shell rc files
// (mise, starship, etc.) can otherwise consume keystrokes from
// `send-keys` via interactive prompts ("mise: trust this config?")
// and silently mangle the command (e.g. eating the leading "open" of
// "opencode" so the shell only sees "code --port 0" — see the bug
// referenced in the comment below).
//
// Pass an empty `command` to start an ordinary login shell.
type tmuxRunner struct {
	listSessions    func() ([]tmuxSession, error)
	listWindows     func(sessionName string) ([]tmuxWindow, error)
	newSession      func(name, directory, command string) error
	newNamedSession func(sessionName, windowName, directory, command string) error
	newWindow       func(name, directory, command string) error
	newNamedWindow  func(sessionName, windowName, directory, command string) error
	killSession     func(name string) error
	killWindow      func(target string) error
}

var defaultTmuxRunner = tmuxRunner{
	listSessions: listTmuxSessions,
	listWindows:  listTmuxWindows,
	newSession: func(name, directory, command string) error {
		args := []string{"new-session", "-d", "-s", name, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	newNamedSession: func(sessionName, windowName, directory, command string) error {
		args := []string{"new-session", "-d", "-s", sessionName, "-n", windowName, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	newWindow: func(name, directory, command string) error {
		// -d: create the window without switching the session's active
		// window, so a client already attached to this session keeps its
		// current view instead of jumping to the freshly launched window.
		args := []string{"new-window", "-d", "-t", name, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	newNamedWindow: func(sessionName, windowName, directory, command string) error {
		args := []string{"new-window", "-d", "-t", sessionName, "-n", windowName, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	killSession: func(name string) error {
		return exec.Command("tmux", "kill-session", "-t", name).Run()
	},
	killWindow: func(target string) error {
		return exec.Command("tmux", "kill-window", "-t", target).Run()
	},
}

// opencodeCommand is the shell command tmux runs in the worktree
// window. We invoke it via `sh -lc` so the user's PATH is initialised
// (opencode is installed via a mise/asdf/homebrew shim that depends on
// shell init), but pass the command as a single literal argument so
// nothing in the rc file (mise, starship, …) can race for keystrokes.
const opencodeCommand = "exec opencode --port 0"

// launchOpencodeInTmux finds or creates a tmux session named after the given
// directory, opens a new window in it, and runs `opencode --port 0` there.
// It returns the name of the tmux session that was used/created.
//
// This is the original (non-idempotent) launcher kept for callers that
// explicitly want a fresh window every time.
func launchOpencodeInTmux(directory string) (string, error) {
	name, _, err := launchOpencodeInTmuxWith(defaultTmuxRunner, directory, false)
	if err == nil {
		opencode.InvalidateOpenCodePortCache()
	}
	return name, err
}

// launchOpencodeInProjectTmuxWindow launches a worktree session inside
// the shared "ocman-worktree" tmux session, under a named window rooted
// at the worktree directory.
//
// This matches the intended UX for /wt:
//   - all worktree windows live in one dedicated session, so launches
//     never disturb a project's own tmux session
//   - one named window per worktree session, qualified by project so
//     worktrees from different projects don't collide
//
// projectDirectory is retained in the signature for compatibility and to
// disambiguate the window name; the worktree no longer attaches to the
// project's session.
//
// The function is idempotent: if the named window already exists in the
// worktree session, it returns the `session:window` target with
// launched=false and does not re-send `opencode --port 0`.
func launchOpencodeInProjectTmuxWindow(projectDirectory, worktreeDirectory string) (target string, launched bool, err error) {
	target, launched, err = launchOpencodeInProjectTmuxWindowWith(defaultTmuxRunner, projectDirectory, worktreeDirectory)
	if err == nil && launched {
		opencode.InvalidateOpenCodePortCache()
	}
	return target, launched, err
}

// launchOpencodeInTmuxWith is the shared core for both launcher
// variants. When idempotent=true and the session already exists, it
// short-circuits without creating a new window, and returns
// launched=false. Otherwise it creates the session (or opens a new
// window in an existing one) and runs `opencode --port 0` directly as
// the pane's foreground command — bypassing the user's shell rc files
// so interactive prompts in mise/starship/etc. can't race against
// `send-keys`.
func launchOpencodeInTmuxWith(r tmuxRunner, directory string, idempotent bool) (string, bool, error) {
	sessionName := tmuxSessionNameForPath(directory)

	// tmux treats `:` and whitespace specially in target identifiers
	// (session:window[.pane]), so a directory like /home/u/my:project
	// would derive a session name tmux silently mis-targets. Validate
	// against the stricter component allowlist (no `:` permitted) so
	// the derived name can never collide with the target separator.
	if !validTmuxComponent.MatchString(sessionName) {
		return "", false, fmt.Errorf("derived tmux session name %q contains invalid characters", sessionName)
	}

	sessionExists := false
	if existing, err := r.listSessions(); err == nil {
		for _, ts := range existing {
			if ts.Name == sessionName {
				sessionExists = true
				break
			}
		}
	}

	if idempotent && sessionExists {
		return sessionName, false, nil
	}

	if !sessionExists {
		if err := r.newSession(sessionName, directory, opencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-session: %w", err)
		}
	} else {
		if err := r.newWindow(sessionName, directory, opencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-window: %w", err)
		}
	}

	return sessionName, true, nil
}

// launchOpencodeInProjectTmuxWindowWith is the shared implementation of
// launchOpencodeInProjectTmuxWindow. It reuses the existing tmux session
// for projectDirectory and creates/reuses a named window for
// worktreeDirectory.
func launchOpencodeInProjectTmuxWindowWith(r tmuxRunner, projectDirectory, worktreeDirectory string) (string, bool, error) {
	_ = projectDirectory // window name carries the project; no project session lookup needed.
	existingSessions, err := r.listSessions()
	if err != nil {
		return "", false, fmt.Errorf("listing tmux sessions: %w", err)
	}
	// The derived window name is built from worktreeDirectory and could
	// in theory contain `:` or other characters tmux would mis-parse —
	// validate before use against the stricter component allowlist.
	windowName := tmuxWindowNameForDirectory(worktreeDirectory)
	if !validTmuxComponent.MatchString(windowName) {
		return "", false, fmt.Errorf("derived tmux window name %q contains invalid characters", windowName)
	}
	target := worktreeTmuxSession + ":" + windowName

	sessionExists := false
	for _, ts := range existingSessions {
		if ts.Name == worktreeTmuxSession {
			sessionExists = true
			break
		}
	}

	// First worktree: create the shared session with this window as its
	// only (named) window. The session is created detached so it never
	// steals focus from whatever the user is currently attached to.
	if !sessionExists {
		if err := r.newNamedSession(worktreeTmuxSession, windowName, worktreeDirectory, opencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-session: %w", err)
		}
		return target, true, nil
	}

	windows, err := r.listWindows(worktreeTmuxSession)
	if err != nil {
		return "", false, fmt.Errorf("listing tmux windows: %w", err)
	}
	for _, w := range windows {
		if w.Name == windowName {
			return target, false, nil
		}
	}

	if err := r.newNamedWindow(worktreeTmuxSession, windowName, worktreeDirectory, opencodeCommand); err != nil {
		return "", false, fmt.Errorf("tmux new-window: %w", err)
	}
	return target, true, nil
}

func restartOpencodeInTmux(directory string) (string, error) {
	target, err := restartOpencodeInTmuxWith(defaultTmuxRunner, directory)
	if err == nil {
		opencode.InvalidateOpenCodePortCache()
	}
	return target, err
}

func restartOpencodeInTmuxWith(r tmuxRunner, directory string) (string, error) {
	existingSessions, err := r.listSessions()
	if err != nil {
		return "", fmt.Errorf("listing tmux sessions: %w", err)
	}
	want := filepath.Clean(directory)
	for _, ts := range existingSessions {
		windows, err := r.listWindows(ts.Name)
		if err != nil {
			return "", fmt.Errorf("listing tmux windows: %w", err)
		}
		for _, win := range windows {
			if filepath.Clean(win.Path) != want || !tmuxWindowRunsOpencode(win) {
				continue
			}
			target := ts.Name + ":" + win.Name
			if ts.Windows <= 1 {
				if err := r.killSession(ts.Name); err != nil {
					return "", fmt.Errorf("tmux kill-session: %w", err)
				}
				name, _, err := launchOpencodeInTmuxWith(r, directory, false)
				return name, err
			}
			if err := r.killWindow(target); err != nil {
				return "", fmt.Errorf("tmux kill-window: %w", err)
			}
			if err := r.newNamedWindow(ts.Name, win.Name, directory, opencodeCommand); err != nil {
				return "", fmt.Errorf("tmux new-window: %w", err)
			}
			return target, nil
		}
	}
	return "", errNoManagedOpencodePane
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
	// The tmux-availability guard lives in this outer wrapper rather than
	// in handleTmuxSwitchWith so unit tests (which call the *With variant
	// directly with a stub runner) can exercise the handler on CI runners
	// that have no tmux binary installed.
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	s.handleTmuxSwitchWith(w, r, defaultTmuxSwitchRunner)
}

func (s *Server) handleTmuxSwitchWith(w http.ResponseWriter, r *http.Request, runner tmuxSwitchRunner) {
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

	// Validate the target actually exists to avoid passing arbitrary
	// strings to tmux. Targets may be either a plain session name or a
	// `session:window` pair (used by worktree sessions, which open in a
	// named window inside the existing project session).
	existingSessions, err := runner.listSessions()
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
		windows, err := runner.listWindows(sessionName)
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
	existingClients, err := runner.listClients()
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
	if !validTTYPath.MatchString(clientTTY) {
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

	if err := runner.switchClient(clientTTY, req.Session); err != nil {
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
