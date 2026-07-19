package ocruntime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

func TestNativeRuntimeLaunchUsesExistingTmuxSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "src/repo")
	session := tmux.SessionNameForPath(repo)

	originalRunner := tmux.DefaultRunner
	t.Cleanup(func() { tmux.DefaultRunner = originalRunner })

	var command string
	tmux.DefaultRunner = tmux.Runner{
		ListSessions: func() ([]tmux.Session, error) {
			return []tmux.Session{{Name: session, ResolvedPath: repo}}, nil
		},
		NewWindowEnv: func(_, _, gotCommand string, _ map[string]string) error {
			command = gotCommand
			return nil
		},
	}

	if _, err := NewNativeRuntime().Launch(context.Background(), LaunchSpec{
		RepoRoot: repo,
		Port:     43210,
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if want := tmux.OpencodeCommandForPort(43210); command != want {
		t.Fatalf("managed command = %q, want %q", command, want)
	}
}
