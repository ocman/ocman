// Package term implements ocman's in-app browser terminals: named
// windows inside one dedicated tmux session, plus the PTY bridge that
// attaches a hostsvc.TermConn to a window. The HTTP/WebSocket layer
// lives in internal/server; this package owns the tmux call sites.
package term

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/creack/pty"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// SessionName is the single dedicated tmux session that hosts every
// in-app terminal window. All terminals — across every project and
// worktree — live as named windows inside this one session, so ocman
// never scatters windows into the user's project sessions and never
// leaks per-viewer grouped sessions (the previous model created an
// ephemeral `ocman-view-<uuid>` session per WebSocket, which leaked on
// restart). Window names encode which directory each terminal belongs
// to; see termWindowName.
const SessionName = "ocman-term"

// AttachLocalPTY is the local Host's TermAttach: it ensures the target
// window exists in the ocman tmux session, opens a PTY attached to it,
// and bridges bytes/resizes to conn until either side closes. This is
// the direct-tmux path; the remote Host tunnels the same conn over gRPC
// to the owner, which runs its own AttachLocalPTY.
func AttachLocalPTY(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	if !tmux.IsAvailable() {
		return fmt.Errorf("tmux is not available")
	}
	windowName := req.Window
	if windowName != "" {
		if err := ensureOcmanSession(); err != nil {
			return fmt.Errorf("ensuring terminal session: %w", err)
		}
		if !termWindowExists(windowName) {
			return fmt.Errorf("terminal window not found")
		}
	} else {
		// No explicit window: reuse the first window for dir, or create
		// one. ensureTermWindow also ensures the ocman session exists.
		win, err := ensureTermWindow(req.Dir)
		if err != nil {
			return fmt.Errorf("ensuring dedicated terminal window: %w", err)
		}
		windowName = win
	}

	// Attach a PTY directly to the ocman session, selecting the target
	// window. We rely on the session's `window-size manual` (set in
	// ensureOcmanSession) plus per-window resize-window calls below so
	// each viewer sizes its own window independently.
	target := SessionName + ":" + windowName
	args := []string{"attach-session", "-t", target}
	if req.Readonly {
		args = append(args, "-r")
	}
	cmd := exec.Command("tmux", args...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("starting pty for tmux attach: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Tear down the tmux client process and PTY when the connection
	// closes from either side. Detaching the client does NOT kill the
	// window, so the shell and its scrollback survive reconnects.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// PTY -> conn: server output to the viewer.
	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// conn -> PTY: viewer keystrokes + resize control frames.
	for {
		frame, readErr := conn.Recv()
		if readErr != nil {
			return nil
		}
		if frame.Resize != nil {
			rz := frame.Resize
			if rz.Rows > 0 && rz.Cols > 0 {
				// Size the PTY (the tmux client) ...
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rz.Rows, Cols: rz.Cols})
				// ... and the specific window, so this viewer's size
				// doesn't depend on other clients attached to the shared
				// ocman session (window-size is manual).
				_ = exec.Command("tmux", "resize-window", "-t", target,
					"-x", strconv.Itoa(int(rz.Cols)), "-y", strconv.Itoa(int(rz.Rows))).Run()
			}
			continue
		}
		if len(frame.Data) > 0 && !req.Readonly {
			_, _ = ptmx.Write(frame.Data)
		}
	}
}

// ── ocman session management ─────────────────────────────────────────

// ocmanSessionExists reports whether the dedicated terminal session is
// currently running.
func ocmanSessionExists() bool {
	return exec.Command("tmux", "has-session", "-t", SessionName).Run() == nil
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
		"-s", SessionName, "-n", "_ocman_placeholder").Run(); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	for _, opt := range [][]string{
		{"set-option", "-t", SessionName, "window-size", "manual"},
		{"set-option", "-t", SessionName, "status", "off"},
		{"set-option", "-t", SessionName, "pane-border-status", "off"},
		// Don't let the placeholder/idle window auto-rename and confuse
		// our name-based tracking.
		{"set-option", "-t", SessionName, "allow-rename", "off"},
		{"set-option", "-t", SessionName, "automatic-rename", "off"},
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

// WindowPrefix is the `ocman-<hash>-` stem shared by every terminal
// window for a directory.
func WindowPrefix(dir string) string {
	return "ocman-" + dirHash(dir) + "-"
}

// IsWindowForDir reports whether name is a well-formed terminal
// window belonging to dir.
func IsWindowForDir(name, dir string) bool {
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
	out, err := exec.Command("tmux", "list-windows", "-t", SessionName,
		"-F", "#{window_name}").Output()
	if err != nil {
		if tmux.IsServerNotRunningError(err) {
			return nil, nil
		}
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
	prefix := WindowPrefix(dir)
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

// CreateWindow creates a new terminal window for dir in the ocman
// session, rooted at dir, with the lowest free index. Ensures the ocman
// session exists first. Returns the new window name.
//
// The window hosts an ordinary login shell so the user gets a real
// interactive prompt. It is persistent: detaching a viewer does not kill
// it, so shell state/history survive reconnects.
func CreateWindow(dir string) (string, error) {
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
	windowName := WindowPrefix(dir) + strconv.Itoa(idx)
	if !tmux.ValidComponent.MatchString(windowName) {
		return "", fmt.Errorf("derived terminal window name %q is invalid", windowName)
	}
	// Create detached so we never steal focus; viewers attach + select
	// it themselves.
	if err := exec.Command("tmux", "new-window", "-d",
		"-t", SessionName, "-n", windowName, "-c", dir).Run(); err != nil {
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
	return CreateWindow(dir)
}

// ── local Host terminal-window deps ──────────────────────────────────
//
// These package-level functions are wired into hostsvc/local.Deps so the
// local Host owns the tmux call sites (AD-16). The remote Host proxies
// the same three operations + TermAttach over gRPC to the owning remote,
// which runs these against its own tmux.

// Windows lists the terminal windows for dir. No ocman session
// yet means no terminals — return an empty slice so the UI shows a clean
// "+" state rather than erroring.
func Windows(dir string) ([]hostsvc.TermWindow, error) {
	if !ocmanSessionExists() {
		return []hostsvc.TermWindow{}, nil
	}
	return listTermWindowInfo(dir)
}

// KillWindow kills the named terminal window for dir. The window
// is re-validated as belonging to dir here so a remote can't be asked to
// kill an arbitrary window. Returns an error when the window doesn't
// exist so the handler can surface a 404-equivalent.
func KillWindow(dir, window string) error {
	if !IsWindowForDir(window, dir) || !termWindowExists(window) {
		return fmt.Errorf("terminal window not found")
	}
	return exec.Command("tmux", "kill-window", "-t",
		SessionName+":"+window).Run()
}

// ── window titles ────────────────────────────────────────────────────

// termWindow is a dedicated terminal window with a display title derived
// from what's running in it. Alias of hostsvc.TermWindow so the local
// Host deps and the REST handlers share one shape.
type termWindow = hostsvc.TermWindow

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
	out, err := exec.Command("tmux", "list-windows", "-t", SessionName,
		"-F", "#{window_name}\t#{pane_current_command}\t#{pane_title}").Output()
	if err != nil {
		return nil, err
	}
	prefix := WindowPrefix(dir)
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

// SweepLegacySessions removes orphaned ephemeral viewer sessions
// from the previous implementation (`ocman-view-<uuid>`). They could
// never belong to a live connection at startup, so killing them at boot
// self-heals leaks across restarts. Safe no-op when tmux isn't running.
func SweepLegacySessions() {
	if !tmux.IsAvailable() {
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
