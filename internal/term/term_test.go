package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// installFakeTmux prepends a fake tmux binary to PATH whose list-*
// subcommands fail with the given stderr, so tests can simulate
// no-server / permission-denied conditions without a real tmux.
func installFakeTmux(t *testing.T, stderr string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  list-sessions|list-clients|list-windows)
    printf '%%s\n' %q >&2
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
`, stderr)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ── pure naming / hashing helpers ────────────────────────────────────

func TestDirHash_StableAndPathNormalised(t *testing.T) {
	// Same cleaned path -> same hash; trailing slash / "." segments are
	// normalised away by filepath.Clean before hashing.
	a := dirHash("/home/u/proj")
	b := dirHash("/home/u/proj/")
	c := dirHash("/home/u/./proj")
	if a != b || a != c {
		t.Fatalf("expected equal hashes for equivalent paths, got %q %q %q", a, b, c)
	}
	// Different paths -> different hashes (collision would be a bug).
	if dirHash("/home/u/proj") == dirHash("/home/u/other") {
		t.Fatal("distinct paths produced the same hash")
	}
	// Hash is the documented 10 lowercase-hex chars.
	if len(a) != 10 {
		t.Fatalf("expected 10-char hash, got %d (%q)", len(a), a)
	}
	for _, r := range a {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("hash %q contains non-hex char %q", a, r)
		}
	}
}

func TestTermWindowName_ShapeAndValidity(t *testing.T) {
	dir := "/home/u/My Project!"
	prefix := WindowPrefix(dir)
	name := prefix + "1"
	if !termWindowRe.MatchString(name) {
		t.Fatalf("generated window name %q does not match termWindowRe", name)
	}
	// Even an awkward directory must yield a tmux-safe component name
	// (no ':', spaces, etc.) — the hash absorbs all the path nastiness.
	if !tmux.ValidComponent.MatchString(name) {
		t.Fatalf("window name %q is not a valid tmux component", name)
	}
}

func TestTermWindowRe_MatchesAndRejects(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"ocman-a3a758e833-1", true},
		{"ocman-0123456789-42", true},
		{"ocman-a3a758e833-", false},   // no index
		{"ocman-xyz-1", false},         // hash not hex
		{"ocman-a3a758e833", false},    // missing index segment
		{"_ocman_placeholder", false},  // the keep-alive window
		{"ocman-term", false},          // the session name, not a window
		{"wt-feature", false},          // unrelated window
		{"ocman-a3a758e8331-1", false}, // 11-char hash (too long)
		{"ocman-a3a758e83-1", false},   // 9-char hash (too short)
	}
	for _, c := range cases {
		if got := termWindowRe.MatchString(c.name); got != c.want {
			t.Errorf("termWindowRe.MatchString(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTermWindowIndex(t *testing.T) {
	cases := map[string]int{
		"ocman-a3a758e833-1":  1,
		"ocman-a3a758e833-42": 42,
		"not-a-window":        0,
		"_ocman_placeholder":  0,
	}
	for name, want := range cases {
		if got := termWindowIndex(name); got != want {
			t.Errorf("termWindowIndex(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestIsTermWindowForDir(t *testing.T) {
	dir := "/home/u/proj"
	other := "/home/u/other"
	win := WindowPrefix(dir) + "3"

	if !IsWindowForDir(win, dir) {
		t.Errorf("expected %q to belong to %q", win, dir)
	}
	// A window for one dir must not be attributed to another — this is
	// what prevents cross-project kills via the wrong dir.
	if IsWindowForDir(win, other) {
		t.Errorf("window %q wrongly attributed to %q", win, other)
	}
	// Non-terminal windows never belong to any dir.
	for _, bad := range []string{"_ocman_placeholder", "ocman-term", "wt-x", "ocman-zzzzzzzzzz-1"} {
		if IsWindowForDir(bad, dir) {
			t.Errorf("non-terminal window %q wrongly attributed to a dir", bad)
		}
	}
}

// ── title derivation ─────────────────────────────────────────────────

func TestTermWindowTitle(t *testing.T) {
	host, _ := os.Hostname()
	cases := []struct {
		name      string
		cmd       string
		paneTitle string
		want      string
	}{
		{"idle shell -> empty", "zsh", "", ""},
		{"idle shell with hostname pane title -> empty", "bash", host, ""},
		{"running command -> command", "vim", "", "vim"},
		{"running command beats empty title", "npm", "", "npm"},
		{"meaningful OSC title wins over command", "vim", "vim main.go", "vim main.go"},
		{"OSC title equal to command falls back to command", "node", "node", "node"},
		{"OSC path-ish title is kept", "zsh", "~/proj (feat)", "~/proj (feat)"},
		{"both empty -> empty", "", "", ""},
	}
	for _, c := range cases {
		if got := termWindowTitle(c.cmd, c.paneTitle); got != c.want {
			t.Errorf("%s: termWindowTitle(%q,%q) = %q, want %q",
				c.name, c.cmd, c.paneTitle, got, c.want)
		}
	}
}

func TestLooksLikeHostname(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("no hostname available")
	}
	if !looksLikeHostname(host) {
		t.Errorf("expected %q to look like the hostname", host)
	}
	// Titles with spaces / paths are never hostnames.
	for _, s := range []string{"vim main.go", "~/proj", "a/b"} {
		if looksLikeHostname(s) {
			t.Errorf("%q should not look like a hostname", s)
		}
	}
}

func TestAllTermWindowNames_NoTmuxServerReturnsEmpty(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (No such file or directory)")

	names, err := allTermWindowNames()
	if err != nil {
		t.Fatalf("allTermWindowNames() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("allTermWindowNames() = %v, want empty", names)
	}
}

func TestAllTermWindowNames_TmuxPermissionErrorReturnsError(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (Permission denied)")

	_, err := allTermWindowNames()
	if err == nil {
		t.Fatal("allTermWindowNames() error = nil, want error")
	}
}

// TestSweepLegacySessions asserts the boot-time sweep kills exactly the
// legacy `ocman-view-<uuid>` sessions and leaves everything else — the
// user's own sessions and the shared ocman-term session — alone.
func TestSweepLegacySessions(t *testing.T) {
	listErr := errors.New("tmux not running")
	tests := []struct {
		name     string
		sessions []string
		listErr  error
		killErr  error
		want     []string
	}{
		{
			name:     "kills only legacy view sessions",
			sessions: []string{"ocman-view-abc", "my-work", SessionName, "ocman-view-def"},
			want:     []string{"ocman-view-abc", "ocman-view-def"},
		},
		{
			name:     "leaves the shared term session alone",
			sessions: []string{SessionName},
		},
		{
			name:    "list failure is a silent no-op",
			listErr: listErr,
		},
		{
			name:     "kill failure does not abort the sweep",
			sessions: []string{"ocman-view-abc", "ocman-view-def"},
			killErr:  errors.New("session not found"),
			want:     []string{"ocman-view-abc", "ocman-view-def"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var killed []string
			sweepLegacySessions(
				func() ([]string, error) { return tc.sessions, tc.listErr },
				func(name string) error {
					killed = append(killed, name)
					return tc.killErr
				},
			)
			if !slices.Equal(killed, tc.want) {
				t.Fatalf("killed %v, want %v", killed, tc.want)
			}
		})
	}
}

// guard against accidental session-name drift: the session name must
// stay a valid tmux component and must not itself match the window
// regex (which would make the placeholder/session confusable).
func TestOcmanTermSessionName(t *testing.T) {
	if !tmux.ValidComponent.MatchString(SessionName) {
		t.Fatalf("session name %q is not a valid tmux component", SessionName)
	}
	if termWindowRe.MatchString(SessionName) {
		t.Fatalf("session name %q must not match the terminal window regex", SessionName)
	}
	if !strings.HasPrefix(SessionName, "ocman") {
		t.Fatalf("session name %q lost its ocman prefix", SessionName)
	}
}

// ── integration: real tmux create/list/kill lifecycle ────────────────
//
// Exercises the core window-management flow against a real tmux server.
// Uses t.TempDir()-derived directories so the window-name hashes are
// unique to this test and never collide with a developer's live
// terminals in the shared ocman-term session. All windows it creates
// are torn down on cleanup.
func TestTermWindowLifecycle_Integration(t *testing.T) {
	if !tmux.IsAvailable() {
		t.Skip("tmux not available")
	}

	dirA := t.TempDir()
	dirB := t.TempDir()

	// Clean up every window we create for these unique dirs, even on
	// failure. We deliberately don't kill the shared session — other
	// terminals may be live in it.
	cleanup := func() {
		for _, dir := range []string{dirA, dirB} {
			names, err := listTermWindowNames(dir)
			if err != nil {
				continue
			}
			for _, n := range names {
				_ = killWindowForTest(n)
			}
		}
	}
	t.Cleanup(cleanup)
	cleanup() // pre-clean in case a previous run aborted

	// Initially empty.
	if got, err := listTermWindowNames(dirA); err != nil || len(got) != 0 {
		t.Fatalf("listTermWindowNames(dirA) = %v, %v; want empty", got, err)
	}

	// Create two windows for dirA; indices allocate 1, 2.
	w1, err := CreateWindow(dirA)
	if err != nil {
		t.Fatalf("CreateWindow(dirA) #1: %v", err)
	}
	w2, err := CreateWindow(dirA)
	if err != nil {
		t.Fatalf("CreateWindow(dirA) #2: %v", err)
	}
	if termWindowIndex(w1) != 1 || termWindowIndex(w2) != 2 {
		t.Fatalf("expected indices 1,2; got %q(%d) %q(%d)",
			w1, termWindowIndex(w1), w2, termWindowIndex(w2))
	}

	// A window for a *different* dir gets its own hash namespace and a
	// fresh index 1 (independent counters per dir).
	wb, err := CreateWindow(dirB)
	if err != nil {
		t.Fatalf("CreateWindow(dirB): %v", err)
	}
	if termWindowIndex(wb) != 1 {
		t.Fatalf("dirB first window index = %d; want 1", termWindowIndex(wb))
	}

	// list is scoped per dir.
	listA, _ := listTermWindowNames(dirA)
	if len(listA) != 2 {
		t.Fatalf("dirA windows = %v; want 2", listA)
	}
	listB, _ := listTermWindowNames(dirB)
	if len(listB) != 1 || listB[0] != wb {
		t.Fatalf("dirB windows = %v; want [%s]", listB, wb)
	}
	// dirA's windows must not appear under dirB and vice-versa.
	for _, n := range listA {
		if IsWindowForDir(n, dirB) {
			t.Fatalf("dirA window %q wrongly attributed to dirB", n)
		}
	}

	// existence checks.
	if !termWindowExists(w1) {
		t.Fatalf("expected %q to exist", w1)
	}

	// Kill w1; index 1 should now be free and reused by the next create.
	if err := killWindowForTest(w1); err != nil {
		t.Fatalf("kill %q: %v", w1, err)
	}
	if termWindowExists(w1) {
		t.Fatalf("expected %q to be gone after kill", w1)
	}
	w1b, err := CreateWindow(dirA)
	if err != nil {
		t.Fatalf("CreateWindow(dirA) after kill: %v", err)
	}
	if termWindowIndex(w1b) != 1 {
		t.Fatalf("expected freed index 1 to be reused, got %d (%q)", termWindowIndex(w1b), w1b)
	}

	// ensureTermWindow reuses the lowest existing window rather than
	// creating a new one.
	ens, err := ensureTermWindow(dirA)
	if err != nil {
		t.Fatalf("ensureTermWindow(dirA): %v", err)
	}
	if termWindowIndex(ens) != 1 {
		t.Fatalf("ensureTermWindow returned index %d; want existing lowest (1)", termWindowIndex(ens))
	}

	// titles: a freshly spawned shell is idle, so its title is empty
	// (the UI falls back to the tab number).
	infos, err := listTermWindowInfo(dirA)
	if err != nil {
		t.Fatalf("listTermWindowInfo(dirA): %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least one window info")
	}
	// The session exists now.
	if !ocmanSessionExists() {
		t.Fatal("expected ocman-term session to exist after creating windows")
	}
}

// killWindowForTest kills a single terminal window in the shared
// session. Test helper kept separate so the production delete path
// (handler) isn't entangled with test teardown.
func killWindowForTest(name string) error {
	return exec.Command("tmux", "kill-window", "-t", SessionName+":"+name).Run()
}

// fakeTermConn is an in-memory hostsvc.TermConn for driving AttachLocalPTY
// without a WebSocket: Recv replays queued frames then blocks until Close
// (returning io.EOF), Write records PTY output.
type fakeTermConn struct {
	frames chan hostsvc.TermFrame
	closed chan struct{}
	mu     sync.Mutex
	out    []byte
	once   sync.Once
}

func newFakeTermConn(frames ...hostsvc.TermFrame) *fakeTermConn {
	c := &fakeTermConn{frames: make(chan hostsvc.TermFrame, len(frames)+1), closed: make(chan struct{})}
	for _, f := range frames {
		c.frames <- f
	}
	return c
}

func (c *fakeTermConn) Recv() (hostsvc.TermFrame, error) {
	select {
	case f := <-c.frames:
		return f, nil
	case <-c.closed:
		return hostsvc.TermFrame{}, io.EOF
	}
}

func (c *fakeTermConn) Write(p []byte) error {
	c.mu.Lock()
	c.out = append(c.out, p...)
	c.mu.Unlock()
	return nil
}

func (c *fakeTermConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeTermConn) output() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := make([]byte, len(c.out))
	copy(b, c.out)
	return b
}

// TestAttachLocalPTY_Integration drives the PTY bridge against a real
// tmux window: it creates a window, attaches, sends a resize + a command
// that prints a marker, and asserts the marker comes back through the
// TermConn. Covers the local TermAttach path end-to-end.
func TestAttachLocalPTY_Integration(t *testing.T) {
	if !tmux.IsAvailable() {
		t.Skip("tmux not available")
	}
	dir := t.TempDir()
	win, err := CreateWindow(dir)
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	t.Cleanup(func() { _ = killWindowForTest(win) })

	conn := newFakeTermConn(
		hostsvc.TermFrame{Resize: &hostsvc.TermSize{Cols: 100, Rows: 40}},
		hostsvc.TermFrame{Data: []byte("printf OCMAN_MARKER\r")},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attachDone := make(chan error, 1)
	go func() {
		attachDone <- AttachLocalPTY(ctx, hostsvc.TermAttachRequest{Dir: dir, Window: win}, conn)
	}()

	// Poll for the marker to appear in the PTY output, then close.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(conn.output()), "OCMAN_MARKER") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(conn.output()), "OCMAN_MARKER") {
		t.Fatalf("marker not seen in PTY output: %q", conn.output())
	}
	conn.Close()
	select {
	case <-attachDone:
	case <-ctx.Done():
		t.Fatal("AttachLocalPTY did not return after conn close")
	}
}

// ── local Host terminal deps ─────────────────────────────────────────

// KillWindow must refuse a window that doesn't belong to dir
// (wrong hash namespace) before any tmux call, so a remote can't be
// asked to kill an arbitrary window. Runs without a tmux binary.
func TestLocalTermKillWindow_RejectsCrossDir(t *testing.T) {
	dir := t.TempDir()
	err := KillWindow(dir, "ocman-deadbeef00-1") // valid shape, wrong hash
	if err == nil {
		t.Fatal("expected error killing a cross-dir window, got nil")
	}
}

// Windows returns an empty (non-nil) slice when the ocman
// session doesn't exist yet, so the UI shows a clean "+" state rather
// than erroring. With no tmux server running there is no session.
func TestLocalTermWindows_EmptyWithoutSession(t *testing.T) {
	if ocmanSessionExists() {
		t.Skip("ocman-term session already running in this environment")
	}
	wins, err := Windows(t.TempDir())
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	if wins == nil || len(wins) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v", wins)
	}
}
