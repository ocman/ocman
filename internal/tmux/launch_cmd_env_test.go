package tmux

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// LaunchOpencodeCmdEnvWith is the launcher internal/ocruntime uses for
// every managed project instance — i.e. the primary /wt path — so its
// session matching and error wrapping are worth pinning down.
func TestLaunchOpencodeCmdEnvWith(t *testing.T) {
	const cmd = "exec opencode --port 4242"
	env := map[string]string{"OPENCODE_PERMISSION": "{}"}
	launchErr := errors.New("boom")

	tests := []struct {
		name string
		// dir is joined onto $HOME unless absolute.
		dir          string
		existing     []Session
		listErr      error
		idempotent   bool
		newEnvErr    error
		newWinErr    error
		wantLaunched bool
		wantSessions int
		wantWindows  int
		wantErr      string
	}{
		{
			name:         "new session when nothing exists",
			dir:          "src/repo",
			idempotent:   true,
			wantLaunched: true,
			wantSessions: 1,
		},
		{
			name:         "idempotent reuse of an existing session",
			dir:          "src/repo",
			existing:     []Session{{Name: "reused", ResolvedPath: "$DIR"}},
			idempotent:   true,
			wantLaunched: false,
		},
		{
			name:         "new window when the session exists and idempotent is off",
			dir:          "src/repo",
			existing:     []Session{{Name: "reused", ResolvedPath: "$DIR"}},
			wantLaunched: true,
			wantWindows:  1,
		},
		{
			name:         "list failure is treated as no existing session",
			dir:          "src/repo",
			listErr:      errors.New("no server"),
			idempotent:   true,
			wantLaunched: true,
			wantSessions: 1,
		},
		{
			name:       "new-session failure is wrapped",
			dir:        "src/repo",
			idempotent: true,
			newEnvErr:  launchErr,
			wantErr:    "tmux new-session",
		},
		{
			name:      "new-window failure is wrapped",
			dir:       "src/repo",
			existing:  []Session{{Name: "reused", ResolvedPath: "$DIR"}},
			newWinErr: launchErr,
			wantErr:   "tmux new-window",
		},
		{
			// A colon would silently re-target another pane, so the
			// launcher must refuse before shelling out.
			name:    "rejects a session name with invalid characters",
			dir:     "src/repo:weird",
			wantErr: "invalid characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, tc.dir)

			existing := slices.Clone(tc.existing)
			for i := range existing {
				if existing[i].ResolvedPath == "$DIR" {
					existing[i].ResolvedPath = dir
				}
			}

			f := &fakeEnvRunner{
				existing:  existing,
				listErr:   tc.listErr,
				newEnvErr: tc.newEnvErr,
				newWinErr: tc.newWinErr,
			}

			name, launched, err := LaunchOpencodeCmdEnvWith(f.toRunner(), dir, cmd, tc.idempotent, env)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if launched != tc.wantLaunched {
				t.Errorf("launched = %v, want %v", launched, tc.wantLaunched)
			}
			// An existing session's own name wins, so tmux's dot ->
			// underscore skew can't fork a second session.
			wantName := SessionNameForPath(dir)
			if len(existing) > 0 {
				wantName = existing[0].Name
			}
			if name != wantName {
				t.Errorf("name = %q, want %q", name, wantName)
			}
			if got := len(f.newSessionEnv); got != tc.wantSessions {
				t.Errorf("NewSessionEnv calls = %d, want %d", got, tc.wantSessions)
			}
			if got := len(f.newWindowEnv); got != tc.wantWindows {
				t.Errorf("NewWindowEnv calls = %d, want %d", got, tc.wantWindows)
			}
			// The caller-supplied command and env must reach the pane
			// verbatim — that's the whole point of this launcher.
			for _, gotCmd := range append(f.newSessionCmd, f.newWindowCmd...) {
				if gotCmd != cmd {
					t.Errorf("pane command = %q, want %q", gotCmd, cmd)
				}
			}
			for _, gotEnv := range append(f.newSessionEnv, f.newWindowEnv...) {
				if gotEnv["OPENCODE_PERMISSION"] != "{}" {
					t.Errorf("env = %v, want the seeded OPENCODE_PERMISSION", gotEnv)
				}
			}
		})
	}
}

func TestEnvArgs(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "nil map yields no args", env: nil},
		{name: "empty map yields no args", env: map[string]string{}},
		{
			name: "single pair",
			env:  map[string]string{"A": "1"},
			want: []string{"-e", "A=1"},
		},
		{
			// Sorted so the constructed command is deterministic and
			// tests can assert on it.
			name: "multiple pairs are sorted by key",
			env:  map[string]string{"C": "3", "A": "1", "B": "2"},
			want: []string{"-e", "A=1", "-e", "B=2", "-e", "C=3"},
		},
		{
			name: "value containing an equals sign is preserved",
			env:  map[string]string{"OPENCODE_PERMISSION": `{"a":"b=c"}`},
			want: []string{"-e", `OPENCODE_PERMISSION={"a":"b=c"}`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := envArgs(tc.env); !slices.Equal(got, tc.want) {
				t.Fatalf("envArgs(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestOpencodeCommandForPort(t *testing.T) {
	got := OpencodeCommandForPort(4242)
	if got != "exec env NODE_NO_WARNINGS=1 opencode --port 4242" {
		t.Fatalf("OpencodeCommandForPort(4242) = %q", got)
	}
	// Same `exec env … opencode` shape as the --port 0 constant, so the
	// pane behaves identically apart from the port.
	const prefix = "exec env NODE_NO_WARNINGS=1 opencode --port "
	if !strings.HasPrefix(got, prefix) || !strings.HasPrefix(OpencodeCommand, prefix) {
		t.Fatalf("port command %q drifted from %q", got, OpencodeCommand)
	}
}

// TestOpencodeCommandSuppressesNodeWarnings pins the NODE_NO_WARNINGS=1
// prefix on every pane command. Without it opencode's upstream
// MaxListenersExceededWarning leak paints over the TUI on every LLM call
// (see the comment on opencodeNoWarnings), which users report as an ocman
// bug. Both builders must carry it: the launchers that take an env map and
// the ones that don't (LaunchOpencodeWith, NewNamedWindow) share only the
// command string, so that is the single choke point.
func TestOpencodeCommandSuppressesNodeWarnings(t *testing.T) {
	for name, command := range map[string]string{
		"OpencodeCommand":        OpencodeCommand,
		"OpencodeCommandForPort": OpencodeCommandForPort(1234),
	} {
		if !strings.Contains(command, "NODE_NO_WARNINGS=1") {
			t.Errorf("%s = %q; want it to suppress node warnings", name, command)
		}
		// The `env` prefix must not hide the pane from window detection:
		// WindowRunsOpencode reads pane_start_command, which is now the
		// wrapped string. (pane_current_command stays "opencode" because
		// env execs in place rather than forking.)
		if !WindowRunsOpencode(Window{Command: "node", StartCommand: command}) {
			t.Errorf("WindowRunsOpencode(%q) = false; want the pane still recognised", command)
		}
	}
}
