package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// Client represents a connected tmux client.
type Client struct {
	TTY     string `json:"tty"`
	Session string `json:"session"`
	Width   string `json:"width"`
	Height  string `json:"height"`
}

// Session represents a tmux session.
type Session struct {
	Name         string `json:"name"`
	ResolvedPath string `json:"resolvedPath"`
	Windows      int    `json:"windows"`
}

// Window represents one window inside a tmux session.
type Window struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Command      string `json:"command,omitempty"`
	StartCommand string `json:"startCommand,omitempty"`
}

var ErrNoManagedOpencodePane = errors.New("no tmux-managed opencode pane found for session directory")

// ResolveSessionPath expands a tmux session name (which may use ~ and
// underscores in place of dots) into an absolute filesystem path for matching
// against OpenCode session directories.
func ResolveSessionPath(name string) string {
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

// IsAvailable checks whether the tmux binary is on PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// IsServerNotRunningError reports whether err is the error tmux emits when no
// server is running yet (e.g. on a fresh machine before the first session
// is created). This is distinct from "tmux is not installed": the binary
// exists and works, there's just no server to list from. Callers that
// have already verified IsAvailable() should treat this as "tmux is
// available with an empty session/client list" rather than as a failure.
func IsServerNotRunningError(err error) bool {
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

// ValidName matches safe tmux session/client names.
// Allows alphanumeric, hyphens, underscores, dots, forward slashes,
// colons, and tildes (paths used as session names). Colons are allowed
// because user-supplied target identifiers may take the form
// `session:window`.
var ValidName = regexp.MustCompile(`^[a-zA-Z0-9._/~:-]+$`)

// ValidComponent matches a single tmux session or window name
// (no `:` allowed — that's the target separator). Used to validate
// names *derived from filesystem paths* before they're handed to tmux,
// where an embedded `:` would split into session/window and either
// error or silently mis-target.
var ValidComponent = regexp.MustCompile(`^[a-zA-Z0-9._/~-]+$`)

// ValidTTYPath matches /dev/ttysNNN or /dev/pts/N style TTY paths.
var ValidTTYPath = regexp.MustCompile(`^/dev/(ttys?\d+|pts/\d+)$`)

// ListClients returns all connected tmux clients.
func ListClients() ([]Client, error) {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}\t#{client_session}\t#{client_width}\t#{client_height}").Output()
	if err != nil {
		return nil, err
	}

	var clients []Client
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		clients = append(clients, Client{
			TTY:     parts[0],
			Session: parts[1],
			Width:   parts[2],
			Height:  parts[3],
		})
	}
	return clients, nil
}

// ListSessions returns all tmux sessions.
func ListSessions() ([]Session, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_windows}").Output()
	if err != nil {
		return nil, err
	}

	var sessions []Session
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
		sessions = append(sessions, Session{
			Name:         parts[0],
			ResolvedPath: ResolveSessionPath(parts[0]),
			Windows:      windows,
		})
	}
	return sessions, nil
}

// ListWindows returns all windows for the given tmux session. The
// `Path` is the current pane's working directory, which is good enough
// for worktree-window matching because each worktree session runs a
// single opencode process rooted at that directory.
func ListWindows(sessionName string) ([]Window, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_name}\t#{pane_current_path}\t#{pane_current_command}\t#{pane_start_command}").Output()
	if err != nil {
		return nil, err
	}
	var windows []Window
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
		windows = append(windows, Window{Name: parts[0], Path: filepath.Clean(parts[1]), Command: command, StartCommand: startCommand})
	}
	return windows, nil
}

func WindowRunsOpencode(win Window) bool {
	return win.Command == "opencode" || strings.Contains(win.StartCommand, "opencode")
}

// WorktreeSession is the single shared tmux session that collects
// every worktree window across all projects, so worktree launches never
// disturb a project's own session.
const WorktreeSession = "ocman-worktree"

// WindowNameForDirectory derives the named tmux window used for a
// worktree rooted at directory. We qualify it with the parent project so
// worktrees from different projects can share the single
// WorktreeSession without window-name collisions: the worktree slug
// alone (e.g. "feature-x") is only unique within a project. The layout
// under <repo-parent>/.worktrees/<repo>/<slug>/ gives us both segments.
func WindowNameForDirectory(directory string) string {
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

// SwitchClient switches the given tmux client to the given session.
func SwitchClient(clientTTY, targetSession string) error {
	return exec.Command("tmux", "switch-client", "-c", clientTTY, "-t", targetSession).Run()
}

// SwitchRunner abstracts the side-effectful calls used by
// handleTmuxSwitch so that unit tests can inject fakes without
// requiring a real tmux binary.
type SwitchRunner struct {
	ListSessions func() ([]Session, error)
	ListClients  func() ([]Client, error)
	ListWindows  func(sessionName string) ([]Window, error)
	SwitchClient func(clientTTY, targetSession string) error
}

var DefaultSwitchRunner = SwitchRunner{
	ListSessions: ListSessions,
	ListClients:  ListClients,
	ListWindows:  ListWindows,
	SwitchClient: SwitchClient,
}

// SessionNameForPath derives a tmux session name from a directory path.
//
// To match the convention used by existing sessions (e.g.
// "~/src/github.com/NoUseFreak/ocman"), directories under the user's home
// are rendered as a tilde-relative path; directories outside home stay
// absolute. tmux itself replaces dots with underscores when displaying
// the name, so callers see e.g. "~/src/github_com/NoUseFreak/ocman".
//
// Empty/"."/"/" inputs fall back to "opencode" so we never hand tmux an
// invalid name.
func SessionNameForPath(directory string) string {
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

// Runner abstracts the tmux side-effects so unit tests can stub
// them. Production code uses DefaultRunner; tests inject a fake.
//
// `command` arguments to NewSession/NewWindow/NewNamedWindow are passed
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
type Runner struct {
	ListSessions    func() ([]Session, error)
	ListWindows     func(sessionName string) ([]Window, error)
	NewSession      func(name, directory, command string) error
	NewNamedSession func(sessionName, windowName, directory, command string) error
	NewWindow       func(name, directory, command string) error
	NewNamedWindow  func(sessionName, windowName, directory, command string) error
	KillSession     func(name string) error
	KillWindow      func(target string) error
}

var DefaultRunner = Runner{
	ListSessions: ListSessions,
	ListWindows:  ListWindows,
	NewSession: func(name, directory, command string) error {
		args := []string{"new-session", "-d", "-s", name, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	NewNamedSession: func(sessionName, windowName, directory, command string) error {
		args := []string{"new-session", "-d", "-s", sessionName, "-n", windowName, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	NewWindow: func(name, directory, command string) error {
		// -d: create the window without switching the session's active
		// window, so a client already attached to this session keeps its
		// current view instead of jumping to the freshly launched window.
		args := []string{"new-window", "-d", "-t", name, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	NewNamedWindow: func(sessionName, windowName, directory, command string) error {
		args := []string{"new-window", "-d", "-t", sessionName, "-n", windowName, "-c", directory}
		if command != "" {
			args = append(args, command)
		}
		return exec.Command("tmux", args...).Run()
	},
	KillSession: func(name string) error {
		return exec.Command("tmux", "kill-session", "-t", name).Run()
	},
	KillWindow: func(target string) error {
		return exec.Command("tmux", "kill-window", "-t", target).Run()
	},
}

// OpencodeCommand is the shell command tmux runs in the worktree
// window. We invoke it via `sh -lc` so the user's PATH is initialised
// (opencode is installed via a mise/asdf/homebrew shim that depends on
// shell init), but pass the command as a single literal argument so
// nothing in the rc file (mise, starship, …) can race for keystrokes.
const OpencodeCommand = "exec opencode --port 0"

// LaunchOpencode finds or creates a tmux session named after the given
// directory, opens a new window in it, and runs `opencode --port 0` there.
// It returns the name of the tmux session that was used/created.
//
// This is the original (non-idempotent) launcher kept for callers that
// explicitly want a fresh window every time.
func LaunchOpencode(directory string) (string, error) {
	name, _, err := LaunchOpencodeWith(DefaultRunner, directory, false)
	if err == nil {
		opencode.InvalidateOpenCodePortCache()
	}
	return name, err
}

// LaunchWorktreeWindow launches a worktree session inside
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
func LaunchWorktreeWindow(projectDirectory, worktreeDirectory string) (target string, launched bool, err error) {
	target, launched, err = LaunchWorktreeWindowWith(DefaultRunner, projectDirectory, worktreeDirectory)
	if err == nil && launched {
		opencode.InvalidateOpenCodePortCache()
	}
	return target, launched, err
}

// LaunchOpencodeWith is the shared core for both launcher
// variants. When idempotent=true and the session already exists, it
// short-circuits without creating a new window, and returns
// launched=false. Otherwise it creates the session (or opens a new
// window in an existing one) and runs `opencode --port 0` directly as
// the pane's foreground command — bypassing the user's shell rc files
// so interactive prompts in mise/starship/etc. can't race against
// `send-keys`.
func LaunchOpencodeWith(r Runner, directory string, idempotent bool) (string, bool, error) {
	sessionName := SessionNameForPath(directory)

	// tmux treats `:` and whitespace specially in target identifiers
	// (session:window[.pane]), so a directory like /home/u/my:project
	// would derive a session name tmux silently mis-targets. Validate
	// against the stricter component allowlist (no `:` permitted) so
	// the derived name can never collide with the target separator.
	if !ValidComponent.MatchString(sessionName) {
		return "", false, fmt.Errorf("derived tmux session name %q contains invalid characters", sessionName)
	}

	// Match by resolved directory, not by name: tmux replaces dots with
	// underscores in session names (e.g. "github.com" -> "github_com"),
	// so a name-equality check against the dotted name we derive here
	// never matches an existing session and we'd hit `new-session` ->
	// "duplicate session" (exit 1). Comparing resolvedPath sidesteps the
	// dot/underscore skew, and we reuse the *existing* tmux name as the
	// window target so send/new-window can't mis-target.
	sessionExists := false
	wantPath := filepath.Clean(directory)
	if existing, err := r.ListSessions(); err == nil {
		for _, ts := range existing {
			if filepath.Clean(ts.ResolvedPath) == wantPath {
				sessionExists = true
				sessionName = ts.Name
				break
			}
		}
	}

	if idempotent && sessionExists {
		return sessionName, false, nil
	}

	if !sessionExists {
		if err := r.NewSession(sessionName, directory, OpencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-session: %w", err)
		}
	} else {
		if err := r.NewWindow(sessionName, directory, OpencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-window: %w", err)
		}
	}

	return sessionName, true, nil
}

// LaunchWorktreeWindowWith is the shared implementation of
// LaunchWorktreeWindow. It reuses the existing tmux session
// for projectDirectory and creates/reuses a named window for
// worktreeDirectory.
func LaunchWorktreeWindowWith(r Runner, projectDirectory, worktreeDirectory string) (string, bool, error) {
	_ = projectDirectory // window name carries the project; no project session lookup needed.
	existingSessions, err := r.ListSessions()
	if err != nil {
		return "", false, fmt.Errorf("listing tmux sessions: %w", err)
	}
	// The derived window name is built from worktreeDirectory and could
	// in theory contain `:` or other characters tmux would mis-parse —
	// validate before use against the stricter component allowlist.
	windowName := WindowNameForDirectory(worktreeDirectory)
	if !ValidComponent.MatchString(windowName) {
		return "", false, fmt.Errorf("derived tmux window name %q contains invalid characters", windowName)
	}
	target := WorktreeSession + ":" + windowName

	sessionExists := false
	for _, ts := range existingSessions {
		if ts.Name == WorktreeSession {
			sessionExists = true
			break
		}
	}

	// First worktree: create the shared session with this window as its
	// only (named) window. The session is created detached so it never
	// steals focus from whatever the user is currently attached to.
	if !sessionExists {
		if err := r.NewNamedSession(WorktreeSession, windowName, worktreeDirectory, OpencodeCommand); err != nil {
			return "", false, fmt.Errorf("tmux new-session: %w", err)
		}
		return target, true, nil
	}

	windows, err := r.ListWindows(WorktreeSession)
	if err != nil {
		return "", false, fmt.Errorf("listing tmux windows: %w", err)
	}
	for _, w := range windows {
		if w.Name == windowName {
			return target, false, nil
		}
	}

	if err := r.NewNamedWindow(WorktreeSession, windowName, worktreeDirectory, OpencodeCommand); err != nil {
		return "", false, fmt.Errorf("tmux new-window: %w", err)
	}
	return target, true, nil
}

func RestartOpencode(directory string) (string, error) {
	target, err := RestartOpencodeWith(DefaultRunner, directory)
	if err == nil {
		opencode.InvalidateOpenCodePortCache()
	}
	return target, err
}

func RestartOpencodeWith(r Runner, directory string) (string, error) {
	existingSessions, err := r.ListSessions()
	if err != nil {
		return "", fmt.Errorf("listing tmux sessions: %w", err)
	}
	want := filepath.Clean(directory)
	for _, ts := range existingSessions {
		windows, err := r.ListWindows(ts.Name)
		if err != nil {
			return "", fmt.Errorf("listing tmux windows: %w", err)
		}
		for _, win := range windows {
			if filepath.Clean(win.Path) != want || !WindowRunsOpencode(win) {
				continue
			}
			target := ts.Name + ":" + win.Name
			if ts.Windows <= 1 {
				if err := r.KillSession(ts.Name); err != nil {
					return "", fmt.Errorf("tmux kill-session: %w", err)
				}
				name, _, err := LaunchOpencodeWith(r, directory, false)
				return name, err
			}
			if err := r.KillWindow(target); err != nil {
				return "", fmt.Errorf("tmux kill-window: %w", err)
			}
			if err := r.NewNamedWindow(ts.Name, win.Name, directory, OpencodeCommand); err != nil {
				return "", fmt.Errorf("tmux new-window: %w", err)
			}
			return target, nil
		}
	}
	return "", ErrNoManagedOpencodePane
}
