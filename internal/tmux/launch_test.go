package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeTmuxRunner records every call so tests can assert on which
// tmux side-effects fired (or didn't), including the command that was
// passed to tmux as the pane's foreground process.
type fakeTmuxRunner struct {
	existing []Session

	listErr error

	newSessionCalls    []string
	newSessionCommands []string
	newSessionEnv      []map[string]string
	newSessionErr      error

	newWindowCalls    []string
	newWindowCommands []string
	newWindowEnv      []map[string]string
	newWindowErr      error
}

func (f *fakeTmuxRunner) toRunner() Runner {
	return Runner{
		ListSessions: func(context.Context) ([]Session, error) {
			if f.listErr != nil {
				return nil, f.listErr
			}
			return f.existing, nil
		},
		NewSessionEnv: func(_ context.Context, name, _, command string, env map[string]string) error {
			f.newSessionCalls = append(f.newSessionCalls, name)
			f.newSessionCommands = append(f.newSessionCommands, command)
			f.newSessionEnv = append(f.newSessionEnv, env)
			return f.newSessionErr
		},
		NewWindowEnv: func(_ context.Context, name, _, command string, env map[string]string) error {
			f.newWindowCalls = append(f.newWindowCalls, name)
			f.newWindowCommands = append(f.newWindowCommands, command)
			f.newWindowEnv = append(f.newWindowEnv, env)
			return f.newWindowErr
		},
	}
}

// launchWith runs the launcher LaunchOpencode uses, with a fake runner.
func launchWith(ctx context.Context, r Runner, directory string, idempotent bool) (string, bool, error) {
	return LaunchOpencodeCmdEnvWith(ctx, r, directory, OpencodeCommand, idempotent, nil)
}

// TestLaunchOpencodeInTmuxWith_MatchesDottedPathAgainstUnderscoreName
// is a regression test for the "duplicate session" bug: tmux stores
// session names with dots replaced by underscores, so an existing
// session for /…/github.com/… is listed as "…/github_com/…". Matching
// by name against our dotted derivation missed it and tried
// `new-session`, which tmux rejected as a duplicate (exit 1). We must
// match on resolvedPath and reuse the existing (underscore) name.
func TestLaunchOpencodeInTmuxWith_MatchesDottedPathAgainstUnderscoreName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/github.com/acme/infra")
	underscoreName := "~/src/github_com/acme/infra" // as tmux would report it

	f := &fakeTmuxRunner{
		existing: []Session{{Name: underscoreName, ResolvedPath: dir}},
	}

	name, launched, err := launchWith(t.Context(), f.toRunner(), dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !launched {
		t.Errorf("launched = false; want true")
	}
	if name != underscoreName {
		t.Errorf("name = %q; want existing tmux name %q", name, underscoreName)
	}
	if len(f.newSessionCalls) != 0 {
		t.Errorf("NewSession called %d times; want 0 (must reuse existing session)", len(f.newSessionCalls))
	}
	if len(f.newWindowCalls) != 1 {
		t.Errorf("NewWindow calls = %d; want 1", len(f.newWindowCalls))
	}
	// The window must target the existing (underscore) name, not our
	// dotted derivation, or tmux would mis-target / re-collide.
	if len(f.newWindowCalls) == 1 && f.newWindowCalls[0] != underscoreName {
		t.Errorf("NewWindow target = %q; want %q", f.newWindowCalls[0], underscoreName)
	}
}

// TestLaunchOpencodeInTmuxWith_MatchesMixedUnderscoreDotSegment is a
// regression test for the duplicate-session bug on a segment holding
// both an underscore and a dot: "my_app.v2" is named "my_app_v2", which
// ResolveSessionPath cannot reverse (it tries only "my.app.v2"). The
// existing session must still be found, by its derived name.
func TestLaunchOpencodeInTmuxWith_MatchesMixedUnderscoreDotSegment(t *testing.T) {
	cases := []struct {
		name       string
		idempotent bool
		launched   bool
		newWindows int
	}{
		{"idempotent reuses", true, false, 0},
		{"non-idempotent opens a window", false, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, "src", "my_app.v2")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			existing := SessionNameForPath(dir)
			f := &fakeTmuxRunner{
				// As listSessions would report it.
				existing: []Session{{Name: existing, ResolvedPath: ResolveSessionPath(existing), Path: dir}},
			}
			name, launched, err := launchWith(t.Context(), f.toRunner(), dir, tc.idempotent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != existing || launched != tc.launched {
				t.Errorf("got (%q, %v), want (%q, %v)", name, launched, existing, tc.launched)
			}
			if len(f.newSessionCalls) != 0 {
				t.Errorf("NewSession calls = %v; want none (duplicate session name)", f.newSessionCalls)
			}
			if len(f.newWindowCalls) != tc.newWindows {
				t.Errorf("NewWindow calls = %v; want %d", f.newWindowCalls, tc.newWindows)
			}
		})
	}
}

// Distinct directories can derive one session name ("my_app.v2" and
// "my.app_v2" are both "my_app_v2"; "my_app" resolves the name of
// "my.app"). Matching by name alone reused the other checkout's session, so
// two projects shared one runtime ID and stopping one killed the other. The
// session's own start directory decides: a colliding session is left alone
// and this directory gets a stable, distinct name.
func TestLaunchOpencodeInTmuxWith_IsolatesCollidingDerivedNames(t *testing.T) {
	cases := []struct {
		name   string
		owner  string // directory the existing session was started in
		target string // directory being launched
	}{
		{"mixed segments", "repos/my_app.v2", "repos/my.app_v2"},
		{"mixed segments reversed", "repos/my.app_v2", "repos/my_app.v2"},
		{"resolved path points at the target", "repos/my.app", "repos/my_app"},
	}
	for _, tc := range cases {
		for _, idempotent := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s idempotent=%v", tc.name, idempotent), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				owner, target := filepath.Join(home, tc.owner), filepath.Join(home, tc.target)
				for _, dir := range []string{owner, target} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				taken := SessionNameForPath(owner)
				if SessionNameForPath(target) != taken {
					t.Fatalf("test needs colliding names: %q vs %q", SessionNameForPath(target), taken)
				}
				f := &fakeTmuxRunner{existing: []Session{{Name: taken, ResolvedPath: ResolveSessionPath(taken), Path: owner, Windows: 1}}}

				name, launched, err := launchWith(t.Context(), f.toRunner(), target, idempotent)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if name == taken || !launched {
					t.Fatalf("got (%q, %v); want a new session distinct from %q", name, launched, taken)
				}
				if !ValidComponent.MatchString(name) {
					t.Fatalf("name %q is not a valid tmux component", name)
				}
				if len(f.newSessionCalls) != 1 || f.newSessionCalls[0] != name || len(f.newWindowCalls) != 0 {
					t.Fatalf("new-session %v, new-window %v; want only new-session %q", f.newSessionCalls, f.newWindowCalls, name)
				}

				// Relaunching the target finds its own session again by
				// the same stable name, and the owner still finds its own.
				f.existing = append(f.existing, Session{Name: name, ResolvedPath: ResolveSessionPath(name), Path: target, Windows: 1})
				again, _, err := launchWith(t.Context(), f.toRunner(), target, true)
				if err != nil || again != name {
					t.Fatalf("relaunch = %q, %v; want %q", again, err, name)
				}
				mine, _, err := launchWith(t.Context(), f.toRunner(), owner, true)
				if err != nil || mine != taken {
					t.Fatalf("owner relaunch = %q, %v; want %q", mine, err, taken)
				}
				if len(f.newSessionCalls) != 1 {
					t.Fatalf("new-session calls %v; want the relaunches to reuse", f.newSessionCalls)
				}
			})
		}
	}
}

func TestSessionForDirectory(t *testing.T) {
	sum := sha256.Sum256([]byte("/b"))
	distinct := "x-" + hex.EncodeToString(sum[:6])
	tests := []struct {
		name     string
		existing []Session
		want     string
		exists   bool
		wantErr  bool
	}{
		{"none", nil, "x", false, false},
		{"owned", []Session{{Name: "x", Path: "/b"}}, "x", true, false},
		{"path unknown keeps name match", []Session{{Name: "x"}}, "x", true, false},
		{"owned by resolved path", []Session{{Name: "y", ResolvedPath: "/b", Path: "/b"}}, "y", true, false},
		{"resolved path of another directory", []Session{{Name: "y", ResolvedPath: "/b", Path: "/c"}}, "x", false, false},
		{"taken", []Session{{Name: "x", Path: "/a"}}, distinct, false, false},
		{"taken, distinct owned", []Session{{Name: "x", Path: "/a"}, {Name: distinct, Path: "/b"}}, distinct, true, false},
		{"taken twice", []Session{{Name: "x", Path: "/a"}, {Name: distinct, Path: "/c"}}, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, exists, err := sessionForDirectory(tc.existing, "x", "/b")
			if (err != nil) != tc.wantErr || got != tc.want || exists != tc.exists {
				t.Fatalf("= (%q, %v, %v); want (%q, %v, err=%v)", got, exists, err, tc.want, tc.exists, tc.wantErr)
			}
		})
	}
}
