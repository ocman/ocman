// Package term implements ocman's in-app browser terminals: named
// windows inside a dedicated tmux server and session, plus the PTY bridge that
// attaches a hostsvc.TermConn to a window. The HTTP/WebSocket layer
// lives in internal/server; this package owns the tmux call sites.
package term

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
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

// socketName isolates browser terminals from the user's default tmux server.
const socketName = "ocman-term"

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
		if err := ensureOcmanSession(ctx); err != nil {
			return fmt.Errorf("ensuring terminal session: %w", err)
		}
		exists, err := termWindowExists(ctx, windowName)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("terminal window not found")
		}
	} else {
		// No explicit window: reuse the first window for dir, or create
		// one. ensureTermWindow also ensures the ocman session exists.
		win, err := ensureTermWindow(ctx, req.Dir)
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
	// The attached client is xterm.js, regardless of the server's TERM.
	// Advertise OSC 52 so tmux sends copy-mode text to the browser.
	args := []string{"-L", socketName, "-T", "clipboard", "attach-session", "-t", target}
	if req.Readonly {
		args = append(args, "-r")
	}
	cmd := exec.CommandContext(ctx, "tmux", args...)

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
				_ = tmux.Run(ctx, "-L", socketName, "resize-window", "-t", target,
					"-x", strconv.Itoa(int(rz.Cols)), "-y", strconv.Itoa(int(rz.Rows)))
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
func ocmanSessionExists(ctx context.Context) (bool, error) {
	err := tmux.Run(ctx, "-L", socketName, "has-session", "-t", SessionName)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	return err == nil, nil
}

// ensureOcmanSession creates the dedicated terminal session if it does
// not exist, configured for the single-session model:
//   - created detached, with one throwaway initial window we immediately
//     rename out of the terminal namespace so it's never surfaced.
//   - window-size manual: each window is sized explicitly per viewer via
//     resize-window, so multiple browser tabs attached to the shared
//     session don't fight over a single client size.
//   - status off: no tmux chrome leaks into the browser pane.
func ensureOcmanSession(ctx context.Context) error {
	exists, err := ocmanSessionExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	// Create detached. The initial window is named "_ocman_placeholder"
	// so it never matches a terminal window pattern; it keeps the
	// session alive even when no terminals exist yet. (tmux requires at
	// least one window per session.)
	if err := tmux.Run(ctx, "-L", socketName, "new-session", "-d",
		"-s", SessionName, "-n", "_ocman_placeholder"); err != nil {
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
		if err := tmux.Run(ctx, append([]string{"-L", socketName}, opt...)...); err != nil {
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
func allTermWindowNames(ctx context.Context) ([]string, error) {
	out, err := tmux.Output(ctx, "-L", socketName, "list-windows", "-t", SessionName,
		"-F", "#{window_name}")
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
func termWindowExists(ctx context.Context, name string) (bool, error) {
	names, err := allTermWindowNames(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nil
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// listTermWindowNames returns the terminal windows for dir in ascending
// index order.
func listTermWindowNames(ctx context.Context, dir string) ([]string, error) {
	all, err := allTermWindowNames(ctx)
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
func CreateWindow(ctx context.Context, dir string) (string, error) {
	if err := ensureOcmanSession(ctx); err != nil {
		return "", err
	}
	existing, err := listTermWindowNames(ctx, dir)
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
	if err := tmux.Run(ctx, "-L", socketName, "new-window", "-d",
		"-t", SessionName, "-n", windowName, "-c", dir); err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	return windowName, nil
}

// ensureTermWindow returns an existing terminal window for dir, or
// creates the first one when none exist.
func ensureTermWindow(ctx context.Context, dir string) (string, error) {
	if err := ensureOcmanSession(ctx); err != nil {
		return "", err
	}
	existing, err := listTermWindowNames(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("listing terminal windows: %w", err)
	}
	if len(existing) > 0 {
		return existing[0], nil
	}
	return CreateWindow(ctx, dir)
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
func Windows(ctx context.Context, dir string) ([]hostsvc.TermWindow, error) {
	exists, err := ocmanSessionExists(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []hostsvc.TermWindow{}, nil
	}
	return listTermWindowInfo(ctx, dir)
}

// KillWindow kills the named terminal window for dir. The window
// is re-validated as belonging to dir here so a remote can't be asked to
// kill an arbitrary window. Returns an error when the window doesn't
// exist so the handler can surface a 404-equivalent.
func KillWindow(ctx context.Context, dir, window string) error {
	if !IsWindowForDir(window, dir) {
		return fmt.Errorf("terminal window not found")
	}
	exists, err := termWindowExists(ctx, window)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("terminal window not found")
	}
	return tmux.Run(ctx, "-L", socketName, "kill-window", "-t", SessionName+":"+window)
}
