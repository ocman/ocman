package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("hash %q contains non-hex char %q", a, r)
		}
	}
}

func TestTermWindowName_ShapeAndValidity(t *testing.T) {
	dir := "/home/u/My Project!"
	prefix := termWindowPrefix(dir)
	name := prefix + "1"
	if !termWindowRe.MatchString(name) {
		t.Fatalf("generated window name %q does not match termWindowRe", name)
	}
	// Even an awkward directory must yield a tmux-safe component name
	// (no ':', spaces, etc.) — the hash absorbs all the path nastiness.
	if !validTmuxComponent.MatchString(name) {
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
		{"ocman-a3a758e833-", false},     // no index
		{"ocman-xyz-1", false},           // hash not hex
		{"ocman-a3a758e833", false},      // missing index segment
		{"_ocman_placeholder", false},    // the keep-alive window
		{"ocman-term", false},            // the session name, not a window
		{"wt-feature", false},            // unrelated window
		{"ocman-a3a758e8331-1", false},   // 11-char hash (too long)
		{"ocman-a3a758e83-1", false},     // 9-char hash (too short)
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
	win := termWindowPrefix(dir) + "3"

	if !isTermWindowForDir(win, dir) {
		t.Errorf("expected %q to belong to %q", win, dir)
	}
	// A window for one dir must not be attributed to another — this is
	// what prevents cross-project kills via the wrong dir.
	if isTermWindowForDir(win, other) {
		t.Errorf("window %q wrongly attributed to %q", win, other)
	}
	// Non-terminal windows never belong to any dir.
	for _, bad := range []string{"_ocman_placeholder", "ocman-term", "wt-x", "ocman-zzzzzzzzzz-1"} {
		if isTermWindowForDir(bad, dir) {
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

// ── handler validation (no tmux required) ────────────────────────────
//
// These exercise the request-validation and method-dispatch paths that
// run before any tmux interaction. They are skipped when a real tmux
// binary is present, because then the handler proceeds past the
// availability guard into tmux calls we don't want in a unit test; the
// guard ordering itself is covered by TestHandleTermWindows_Dispatch.

func TestValidDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		ok   bool
	}{
		{"absolute ok", "/home/u/proj", true},
		{"empty rejected", "", false},
		{"relative rejected", "proj/sub", false},
		{"dot rejected", ".", false},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		if got := validDir(rr, c.dir); got != c.ok {
			t.Errorf("%s: validDir(%q) = %v, want %v (body=%q)",
				c.name, c.dir, got, c.ok, rr.Body.String())
		}
		if !c.ok && rr.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400 on invalid dir, got %d", c.name, rr.Code)
		}
	}
}

// The sub-handlers run their request validation before any tmux call,
// so we can assert the validation paths directly regardless of whether
// a tmux binary is present (the availability guard lives in the parent
// dispatcher handleTermWindows).

func TestHandleTermWindowsList_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/term/windows?dir=relative", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindowsList(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindowsList_MissingDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/term/windows", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindowsList(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing dir, got %d", rr.Code)
	}
}

func TestHandleTermWindowsCreate_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/term/windows",
		strings.NewReader(`{"dir":"relative"}`))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsCreate(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindowsDelete_BadDir(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/term/windows",
		strings.NewReader(`{"dir":"relative","window":"ocman-a3a758e833-1"}`))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsDelete(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTermWindows_MethodNotAllowed(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available; dispatcher returns 503 before method check")
	}
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPut, "/api/term/windows", nil)
	rr := httptest.NewRecorder()
	srv.handleTermWindows(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for PUT, got %d", rr.Code)
	}
}

func TestHandleTermWindowsDelete_RejectsCrossDirWindow(t *testing.T) {
	srv := &Server{}
	// A window name whose hash doesn't match the supplied (valid,
	// absolute) dir must be rejected as not-found, never killed — this
	// is the guard that stops cross-project / arbitrary kills. The
	// mismatch short-circuits before any tmux call, so this runs
	// without a tmux binary.
	dir := t.TempDir()
	foreign := "ocman-deadbeef00-1" // valid shape, wrong hash for dir
	body := `{"dir":"` + dir + `","window":"` + foreign + `"}`
	req := httptest.NewRequest(http.MethodDelete, "/api/term/windows",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleTermWindowsDelete(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-dir window, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSweepLegacyTermSessions_NoPanic ensures the boot-time sweep is
// safe to call regardless of tmux state (it must be a no-op rather than
// erroring when tmux is missing or no legacy sessions exist).
func TestSweepLegacyTermSessions_NoPanic(t *testing.T) {
	// Should never panic or block; result is environment-dependent.
	sweepLegacyTermSessions()
}

// guard against accidental session-name drift: the session name must
// stay a valid tmux component and must not itself match the window
// regex (which would make the placeholder/session confusable).
func TestOcmanTermSessionName(t *testing.T) {
	if !validTmuxComponent.MatchString(ocmanTermSession) {
		t.Fatalf("session name %q is not a valid tmux component", ocmanTermSession)
	}
	if termWindowRe.MatchString(ocmanTermSession) {
		t.Fatalf("session name %q must not match the terminal window regex", ocmanTermSession)
	}
	if !strings.HasPrefix(ocmanTermSession, "ocman") {
		t.Fatalf("session name %q lost its ocman prefix", ocmanTermSession)
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
	if !isTmuxAvailable() {
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
	w1, err := createTermWindow(dirA)
	if err != nil {
		t.Fatalf("createTermWindow(dirA) #1: %v", err)
	}
	w2, err := createTermWindow(dirA)
	if err != nil {
		t.Fatalf("createTermWindow(dirA) #2: %v", err)
	}
	if termWindowIndex(w1) != 1 || termWindowIndex(w2) != 2 {
		t.Fatalf("expected indices 1,2; got %q(%d) %q(%d)",
			w1, termWindowIndex(w1), w2, termWindowIndex(w2))
	}

	// A window for a *different* dir gets its own hash namespace and a
	// fresh index 1 (independent counters per dir).
	wb, err := createTermWindow(dirB)
	if err != nil {
		t.Fatalf("createTermWindow(dirB): %v", err)
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
		if isTermWindowForDir(n, dirB) {
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
	w1b, err := createTermWindow(dirA)
	if err != nil {
		t.Fatalf("createTermWindow(dirA) after kill: %v", err)
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
	return exec.Command("tmux", "kill-window", "-t", ocmanTermSession+":"+name).Run()
}
