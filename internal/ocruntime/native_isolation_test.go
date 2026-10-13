package ocruntime

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

func TestNativeRuntimeTmuxIsolation(t *testing.T) {
	original := tmux.DefaultRunner
	t.Cleanup(func() { tmux.DefaultRunner = original })
	tmux.DefaultRunner = tmux.Runner{
		ListSessions: func(context.Context) ([]tmux.Session, error) {
			t.Fatal("managed launch reached user's tmux runner")
			return nil, nil
		},
	}
	// All side effects use fakes; no real tmux or OpenCode process is started.
	managed := tmux.ManagedRunner
	t.Cleanup(func() { tmux.ManagedRunner = managed })
	var created, windowed, stopped bool
	tmux.ManagedRunner = tmux.Runner{
		ListSessions: func(context.Context) ([]tmux.Session, error) {
			if created {
				return []tmux.Session{{Name: "owned", ResolvedPath: "/owned"}}, nil
			}
			return nil, nil
		},
		NewSessionEnv: func(context.Context, string, string, string, map[string]string) error {
			created = true
			return nil
		},
		NewWindowEnv: func(context.Context, string, string, string, map[string]string) error {
			windowed = true
			return nil
		},
		KillSession: func(context.Context, string) error {
			stopped = true
			return nil
		},
	}
	runtime := NewNativeRuntime()
	inst, err := runtime.Launch(t.Context(), LaunchSpec{RepoRoot: "/owned", Port: 43210})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Launch(t.Context(), LaunchSpec{RepoRoot: "/owned", Port: 43211}); err != nil {
		t.Fatal(err)
	}
	inst.Endpoint = ""
	if err := runtime.Stop(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	if !created || !windowed || !stopped {
		t.Fatalf("managed operations: create=%v window=%v stop=%v", created, windowed, stopped)
	}
}
