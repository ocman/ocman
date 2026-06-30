package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmuxRunner records every call so tests can assert on which
// tmux side-effects fired (or didn't), including the command that was
// passed to tmux as the pane's foreground process.
type fakeTmuxRunner struct {
	existing []tmuxSession
	windows  map[string][]tmuxWindow

	listErr        error
	listWindowsErr error

	newSessionCalls    []string
	newSessionCommands []string
	newSessionErr      error

	newNamedSessionCalls    []string
	newNamedSessionCommands []string
	newNamedSessionErr      error

	newWindowCalls    []string
	newWindowCommands []string
	newWindowErr      error

	newNamedWindowCalls    []string
	newNamedWindowCommands []string
	newNamedWindowErr      error

	killSessionCalls []string
	killSessionErr   error

	killWindowCalls []string
	killWindowErr   error
}

func (f *fakeTmuxRunner) toRunner() tmuxRunner {
	return tmuxRunner{
		listSessions: func() ([]tmuxSession, error) {
			if f.listErr != nil {
				return nil, f.listErr
			}
			return f.existing, nil
		},
		listWindows: func(sessionName string) ([]tmuxWindow, error) {
			if f.listWindowsErr != nil {
				return nil, f.listWindowsErr
			}
			return f.windows[sessionName], nil
		},
		newSession: func(name, _, command string) error {
			f.newSessionCalls = append(f.newSessionCalls, name)
			f.newSessionCommands = append(f.newSessionCommands, command)
			return f.newSessionErr
		},
		newNamedSession: func(sessionName, windowName, _, command string) error {
			f.newNamedSessionCalls = append(f.newNamedSessionCalls, sessionName+":"+windowName)
			f.newNamedSessionCommands = append(f.newNamedSessionCommands, command)
			return f.newNamedSessionErr
		},
		newWindow: func(name, _, command string) error {
			f.newWindowCalls = append(f.newWindowCalls, name)
			f.newWindowCommands = append(f.newWindowCommands, command)
			return f.newWindowErr
		},
		newNamedWindow: func(sessionName, windowName, _, command string) error {
			f.newNamedWindowCalls = append(f.newNamedWindowCalls, sessionName+":"+windowName)
			f.newNamedWindowCommands = append(f.newNamedWindowCommands, command)
			return f.newNamedWindowErr
		},
		killSession: func(name string) error {
			f.killSessionCalls = append(f.killSessionCalls, name)
			return f.killSessionErr
		},
		killWindow: func(target string) error {
			f.killWindowCalls = append(f.killWindowCalls, target)
			return f.killWindowErr
		},
	}
}

// TestLaunchOpencodeInTmuxWith_Idempotent_ReusesExisting verifies the
// AD-4 behaviour: when the target tmux session already exists, the
// idempotent path must NOT create a window and must NOT send keys.
func TestLaunchOpencodeInTmuxWith_Idempotent_ReusesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := tmuxSessionNameForPath(dir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: wantName}},
	}

	name, launched, err := launchOpencodeInTmuxWith(f.toRunner(), dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if launched {
		t.Errorf("launched = true; want false (session pre-existed)")
	}
	if len(f.newSessionCalls) != 0 {
		t.Errorf("newSession called %d times; want 0", len(f.newSessionCalls))
	}
	if len(f.newWindowCalls) != 0 {
		t.Errorf("newWindow called %d times; want 0 (idempotent path skips it)", len(f.newWindowCalls))
	}
}

// TestLaunchOpencodeInTmuxWith_Idempotent_CreatesWhenAbsent verifies
// the idempotent path still creates a session and runs opencode when
// no matching session exists.
func TestLaunchOpencodeInTmuxWith_Idempotent_CreatesWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := tmuxSessionNameForPath(dir)

	f := &fakeTmuxRunner{} // no existing sessions

	name, launched, err := launchOpencodeInTmuxWith(f.toRunner(), dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if !launched {
		t.Errorf("launched = false; want true")
	}
	if len(f.newSessionCalls) != 1 {
		t.Errorf("newSession calls = %d; want 1", len(f.newSessionCalls))
	}
	// The opencode command must be passed at session-creation time so
	// the user's shell rc files (mise/starship) can't race with us
	// over keystrokes — see comment on tmuxRunner.
	if len(f.newSessionCommands) != 1 || f.newSessionCommands[0] != opencodeCommand {
		t.Errorf("newSessionCommands = %v; want [%q]", f.newSessionCommands, opencodeCommand)
	}
}

// TestLaunchOpencodeInTmuxWith_NonIdempotent_OpensNewWindow verifies
// that when idempotent=false (the legacy launcher behaviour), an
// existing session triggers a new window with opencode as its
// foreground command, NOT short-circuit.
func TestLaunchOpencodeInTmuxWith_NonIdempotent_OpensNewWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := tmuxSessionNameForPath(dir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: wantName}},
	}

	name, launched, err := launchOpencodeInTmuxWith(f.toRunner(), dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if !launched {
		t.Errorf("launched = false; want true (non-idempotent always launches)")
	}
	if len(f.newWindowCalls) != 1 {
		t.Errorf("newWindow calls = %d; want 1", len(f.newWindowCalls))
	}
	if len(f.newWindowCommands) != 1 || f.newWindowCommands[0] != opencodeCommand {
		t.Errorf("newWindowCommands = %v; want [%q]", f.newWindowCommands, opencodeCommand)
	}
}

// TestLaunchOpencodeInTmuxWith_PropagatesNewSessionError ensures tmux
// failures are surfaced rather than swallowed.
func TestLaunchOpencodeInTmuxWith_PropagatesNewSessionError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")

	wantErr := errors.New("boom")
	f := &fakeTmuxRunner{newSessionErr: wantErr}

	_, launched, err := launchOpencodeInTmuxWith(f.toRunner(), dir, true)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain missing wantErr: %v", err)
	}
	if launched {
		t.Errorf("launched = true on error; want false")
	}
}

func TestLaunchOpencodeInProjectTmuxWindowWith_CreatesNamedWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "src/github.com/NoUseFreak/ocman")
	worktreeDir := filepath.Join(projectDir, ".worktrees", "ocman", "feature-login")
	windowName := tmuxWindowNameForDirectory(worktreeDir)

	// No ocman-worktree session yet: the first worktree must create the
	// shared session with the worktree as its named window.
	f := &fakeTmuxRunner{}

	target, launched, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != worktreeTmuxSession+":"+windowName {
		t.Errorf("target = %q, want %q", target, worktreeTmuxSession+":"+windowName)
	}
	if !launched {
		t.Errorf("launched = false; want true")
	}
	if len(f.newNamedSessionCalls) != 1 {
		t.Fatalf("newNamedSession calls = %d; want 1", len(f.newNamedSessionCalls))
	}
	if f.newNamedSessionCalls[0] != worktreeTmuxSession+":"+windowName {
		t.Errorf("newNamedSession target = %q, want %q", f.newNamedSessionCalls[0], worktreeTmuxSession+":"+windowName)
	}
	if len(f.newNamedWindowCalls) != 0 {
		t.Errorf("newNamedWindow calls = %d; want 0 on first worktree", len(f.newNamedWindowCalls))
	}
	// Opencode must be the window's foreground command, not a
	// post-create send-keys payload.
	if len(f.newNamedSessionCommands) != 1 || f.newNamedSessionCommands[0] != opencodeCommand {
		t.Errorf("newNamedSessionCommands = %v, want [%q]", f.newNamedSessionCommands, opencodeCommand)
	}
}

func TestLaunchOpencodeInProjectTmuxWindowWith_AddsWindowToExistingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "src/github.com/NoUseFreak/ocman")
	worktreeDir := filepath.Join(projectDir, ".worktrees", "ocman", "feature-login")
	windowName := tmuxWindowNameForDirectory(worktreeDir)

	// Shared session already exists with an unrelated window: a new
	// worktree adds a window, it does not create a session.
	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: worktreeTmuxSession}},
		windows:  map[string][]tmuxWindow{worktreeTmuxSession: {{Name: "wt-other-thing"}}},
	}

	target, launched, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != worktreeTmuxSession+":"+windowName {
		t.Errorf("target = %q, want %q", target, worktreeTmuxSession+":"+windowName)
	}
	if !launched {
		t.Errorf("launched = false; want true")
	}
	if len(f.newNamedSessionCalls) != 0 {
		t.Errorf("newNamedSession calls = %d; want 0 when session exists", len(f.newNamedSessionCalls))
	}
	if len(f.newNamedWindowCalls) != 1 || f.newNamedWindowCalls[0] != worktreeTmuxSession+":"+windowName {
		t.Errorf("newNamedWindowCalls = %v, want [%q]", f.newNamedWindowCalls, worktreeTmuxSession+":"+windowName)
	}
}

func TestLaunchOpencodeInProjectTmuxWindowWith_ReusesExistingWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "src/github.com/NoUseFreak/ocman")
	worktreeDir := filepath.Join(projectDir, ".worktrees", "ocman", "feature-login")
	windowName := tmuxWindowNameForDirectory(worktreeDir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: worktreeTmuxSession}},
		windows:  map[string][]tmuxWindow{worktreeTmuxSession: {{Name: windowName, Path: worktreeDir}}},
	}

	target, launched, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != worktreeTmuxSession+":"+windowName {
		t.Errorf("target = %q, want %q", target, worktreeTmuxSession+":"+windowName)
	}
	if launched {
		t.Errorf("launched = true; want false")
	}
	if len(f.newNamedWindowCalls) != 0 {
		t.Errorf("newNamedWindowCalls = %v; want none", f.newNamedWindowCalls)
	}
}

// TestLaunchOpencodeInTmuxWith_RejectsInvalidDerivedName verifies that a
// directory whose derived session name would confuse tmux's target
// parser (e.g. contains a `:`) is rejected before any tmux command is
// issued. Otherwise tmux would interpret part of the name as a window,
// either erroring or silently targeting the wrong place.
func TestLaunchOpencodeInTmuxWith_RejectsInvalidDerivedName(t *testing.T) {
	// Outside-home path so tmuxSessionNameForPath returns it verbatim
	// and the `:` ends up in the derived name.
	dir := "/var/projects/has:colon"

	f := &fakeTmuxRunner{}

	_, _, err := launchOpencodeInTmuxWith(f.toRunner(), dir, true)
	if err == nil {
		t.Fatal("expected error for derived name with invalid characters")
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("error = %v; want invalid-characters error", err)
	}
	if len(f.newSessionCalls) != 0 || len(f.newWindowCalls) != 0 {
		t.Error("no tmux command should fire when the derived name fails validation")
	}
}

// TestLaunchOpencodeInProjectTmuxWindowWith_RejectsInvalidDerivedWindowName
// verifies the same protection for the per-worktree window-naming path.
func TestLaunchOpencodeInProjectTmuxWindowWith_RejectsInvalidDerivedWindowName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "src/proj")
	projectSessionName := tmuxSessionNameForPath(projectDir)
	// filepath.Base("/a/wt:bad") = "wt:bad", which the windowName
	// derivation prefixes with "wt-" -> "wt-wt:bad". The `:` then
	// trips the validator.
	worktreeDir := "/tmp/wt:bad"

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: projectSessionName, ResolvedPath: projectDir}},
	}

	_, _, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err == nil {
		t.Fatal("expected error for derived window name with invalid characters")
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("error = %v; want invalid-characters error", err)
	}
	if len(f.newNamedWindowCalls) != 0 || len(f.newNamedSessionCalls) != 0 {
		t.Error("no tmux command must fire when the derived window name fails validation")
	}
}

func TestRestartOpencodeInTmuxWith_RestartsMatchingOpencodeWindow(t *testing.T) {
	dir := "/tmp/repo"
	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: "repo", Windows: 2}},
		windows: map[string][]tmuxWindow{"repo": {
			{Name: "editor", Path: dir, Command: "zsh"},
			{Name: "oc", Path: dir, Command: "opencode"},
		}},
	}

	target, err := restartOpencodeInTmuxWith(f.toRunner(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "repo:oc" {
		t.Errorf("target = %q, want repo:oc", target)
	}
	if len(f.killWindowCalls) != 1 || f.killWindowCalls[0] != "repo:oc" {
		t.Errorf("killWindowCalls = %v, want [repo:oc]", f.killWindowCalls)
	}
	if len(f.newNamedWindowCalls) != 1 || f.newNamedWindowCalls[0] != "repo:oc" {
		t.Errorf("newNamedWindowCalls = %v, want [repo:oc]", f.newNamedWindowCalls)
	}
}

func TestRestartOpencodeInTmuxWith_RejectsUnmanagedWindow(t *testing.T) {
	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: "repo", Windows: 1}},
		windows:  map[string][]tmuxWindow{"repo": {{Name: "shell", Path: "/tmp/repo", Command: "zsh"}}},
	}

	_, err := restartOpencodeInTmuxWith(f.toRunner(), "/tmp/repo")
	if !errors.Is(err, errNoManagedOpencodePane) {
		t.Fatalf("err = %v, want errNoManagedOpencodePane", err)
	}
	if len(f.killWindowCalls) != 0 || len(f.killSessionCalls) != 0 {
		t.Fatalf("unexpected kill calls: window=%v session=%v", f.killWindowCalls, f.killSessionCalls)
	}
}

// When the opencode pane is the session's only window, the whole
// session is killed and a fresh one launched (kill-window would also
// kill the session, so this avoids a transient empty session).
func TestRestartOpencodeInTmuxWith_SingleWindowKillsSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "src/repo")
	name := tmuxSessionNameForPath(dir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: name, Windows: 1}},
		windows:  map[string][]tmuxWindow{name: {{Name: "oc", Path: dir, Command: "opencode"}}},
	}

	if _, err := restartOpencodeInTmuxWith(f.toRunner(), dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.killSessionCalls) != 1 || f.killSessionCalls[0] != name {
		t.Errorf("killSessionCalls = %v, want [%s]", f.killSessionCalls, name)
	}
	if len(f.killWindowCalls) != 0 {
		t.Errorf("killWindowCalls = %v, want none (single-window path kills the session)", f.killWindowCalls)
	}
}

// A pane whose foreground process isn't "opencode" but whose tmux
// start command launched opencode (e.g. wrapped in a shell) still
// counts as a managed pane.
func TestRestartOpencodeInTmuxWith_MatchesViaStartCommand(t *testing.T) {
	dir := "/tmp/repo"
	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: "repo", Windows: 2}},
		windows: map[string][]tmuxWindow{"repo": {
			{Name: "editor", Path: dir, Command: "zsh"},
			{Name: "oc", Path: dir, Command: "node", StartCommand: "opencode --port 0"},
		}},
	}

	target, err := restartOpencodeInTmuxWith(f.toRunner(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "repo:oc" {
		t.Errorf("target = %q, want repo:oc", target)
	}
	if len(f.killWindowCalls) != 1 || f.killWindowCalls[0] != "repo:oc" {
		t.Errorf("killWindowCalls = %v, want [repo:oc]", f.killWindowCalls)
	}
}
