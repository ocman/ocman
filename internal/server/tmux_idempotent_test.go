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

	newWindowCalls    []string
	newWindowCommands []string
	newWindowErr      error

	newNamedWindowCalls    []string
	newNamedWindowCommands []string
	newNamedWindowErr      error
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
	projectSessionName := "~/src/github_com/NoUseFreak/ocman"
	windowName := tmuxWindowNameForDirectory(worktreeDir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: projectSessionName, ResolvedPath: projectDir}},
		windows:  map[string][]tmuxWindow{projectSessionName: {}},
	}

	target, launched, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != projectSessionName+":"+windowName {
		t.Errorf("target = %q, want %q", target, projectSessionName+":"+windowName)
	}
	if !launched {
		t.Errorf("launched = false; want true")
	}
	if len(f.newNamedWindowCalls) != 1 {
		t.Fatalf("newNamedWindow calls = %d; want 1", len(f.newNamedWindowCalls))
	}
	if f.newNamedWindowCalls[0] != projectSessionName+":"+windowName {
		t.Errorf("newNamedWindow target = %q, want %q", f.newNamedWindowCalls[0], projectSessionName+":"+windowName)
	}
	// Opencode must be the window's foreground command, not a
	// post-create send-keys payload.
	if len(f.newNamedWindowCommands) != 1 || f.newNamedWindowCommands[0] != opencodeCommand {
		t.Errorf("newNamedWindowCommands = %v, want [%q]", f.newNamedWindowCommands, opencodeCommand)
	}
}

func TestLaunchOpencodeInProjectTmuxWindowWith_ReusesExistingWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "src/github.com/NoUseFreak/ocman")
	worktreeDir := filepath.Join(projectDir, ".worktrees", "ocman", "feature-login")
	projectSessionName := "~/src/github_com/NoUseFreak/ocman"
	windowName := tmuxWindowNameForDirectory(worktreeDir)

	f := &fakeTmuxRunner{
		existing: []tmuxSession{{Name: projectSessionName, ResolvedPath: projectDir}},
		windows:  map[string][]tmuxWindow{projectSessionName: {{Name: windowName, Path: worktreeDir}}},
	}

	target, launched, err := launchOpencodeInProjectTmuxWindowWith(f.toRunner(), projectDir, worktreeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != projectSessionName+":"+windowName {
		t.Errorf("target = %q, want %q", target, projectSessionName+":"+windowName)
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
	if len(f.newNamedWindowCalls) != 0 {
		t.Error("newNamedWindow must not fire when the derived window name fails validation")
	}
}
