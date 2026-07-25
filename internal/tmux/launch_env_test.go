package tmux

import (
	"path/filepath"
	"testing"
)

// TestLaunchOpencodeEnvWith_SeedsEnvOnNewSession verifies that env vars
// are threaded to the pane at session-creation time (so OpenCode can read
// OPENCODE_PERMISSION at launch) and that the idempotent path is honoured.
func TestLaunchOpencodeEnvWith_SeedsEnvOnNewSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := SessionNameForPath(dir)

	f := &fakeEnvRunner{}

	name, launched, err := LaunchOpencodeEnvWith(f.toRunner(), dir, true,
		map[string]string{"OPENCODE_PERMISSION": `{"external_directory":{}}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if !launched {
		t.Error("launched = false; want true (no existing session)")
	}
	if len(f.newSessionEnv) != 1 {
		t.Fatalf("NewSessionEnv calls = %d; want 1", len(f.newSessionEnv))
	}
	if got := f.newSessionEnv[0]["OPENCODE_PERMISSION"]; got != `{"external_directory":{}}` {
		t.Errorf("OPENCODE_PERMISSION = %q; want the seeded JSON", got)
	}
	if f.newSessionCmd[0] != OpencodeCommand {
		t.Errorf("command = %q; want %q", f.newSessionCmd[0], OpencodeCommand)
	}
}

// TestLaunchOpencodeEnvWith_IdempotentReuse confirms that when a session
// for the directory already exists, no launch (and no env seeding) fires.
func TestLaunchOpencodeEnvWith_IdempotentReuse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := SessionNameForPath(dir)

	f := &fakeEnvRunner{existing: []Session{{Name: wantName, ResolvedPath: dir}}}

	name, launched, err := LaunchOpencodeEnvWith(f.toRunner(), dir, true,
		map[string]string{"OPENCODE_PERMISSION": "{}"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if launched {
		t.Error("launched = true; want false (session pre-existed)")
	}
	if len(f.newSessionEnv) != 0 {
		t.Errorf("NewSessionEnv called %d times; want 0", len(f.newSessionEnv))
	}
}

// TestLaunchOpencodeEnvWith_EnvOnNewWindow verifies the env is threaded on
// the new-window path (existing session, non-idempotent).
func TestLaunchOpencodeEnvWith_EnvOnNewWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/repo")
	wantName := SessionNameForPath(dir)

	f := &fakeEnvRunner{existing: []Session{{Name: wantName, ResolvedPath: dir}}}

	_, launched, err := LaunchOpencodeEnvWith(f.toRunner(), dir, false,
		map[string]string{"OPENCODE_PERMISSION": "{}"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !launched {
		t.Error("launched = false; want true")
	}
	if len(f.newWindowEnv) != 1 {
		t.Fatalf("NewWindowEnv calls = %d; want 1", len(f.newWindowEnv))
	}
	if got := f.newWindowEnv[0]["OPENCODE_PERMISSION"]; got != "{}" {
		t.Errorf("OPENCODE_PERMISSION = %q; want {}", got)
	}
}

// fakeEnvRunner records the env-aware runner calls.
type fakeEnvRunner struct {
	existing    []Session
	listErr     error
	newEnvErr   error
	newWinErr   error
	newSessions []string

	newSessionEnv []map[string]string
	newSessionCmd []string
	newWindowEnv  []map[string]string
	newWindowCmd  []string
}

func (f *fakeEnvRunner) toRunner() Runner {
	return Runner{
		ListSessions: func() ([]Session, error) { return f.existing, f.listErr },
		ListWindows:  func(string) ([]Window, error) { return nil, nil },
		NewSessionEnv: func(name, _, command string, env map[string]string) error {
			if f.newEnvErr != nil {
				return f.newEnvErr
			}
			f.newSessions = append(f.newSessions, name)
			f.newSessionEnv = append(f.newSessionEnv, env)
			f.newSessionCmd = append(f.newSessionCmd, command)
			return nil
		},
		NewWindowEnv: func(_, _, command string, env map[string]string) error {
			if f.newWinErr != nil {
				return f.newWinErr
			}
			f.newWindowEnv = append(f.newWindowEnv, env)
			f.newWindowCmd = append(f.newWindowCmd, command)
			return nil
		},
	}
}
