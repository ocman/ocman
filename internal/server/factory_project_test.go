package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestFactoryCommonRootUsesMainWorktree(t *testing.T) {
	main := filepath.Join(t.TempDir(), "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", main}, {"-C", main, "config", "user.email", "test@example.com"}, {"-C", main, "config", "user.name", "Test"}, {"-C", main, "commit", "--allow-empty", "-m", "initial"}} {
		if err := gitexec.Command(context.Background(), args...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	if err := gitexec.Command(context.Background(), "-C", main, "worktree", "add", worktree).Run(); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	got, err := git.ResolveMainRepoRoot(context.Background(), worktree)
	if err != nil || got != want {
		t.Fatalf("ResolveMainRepoRoot = %q, %v; want %q", got, err, want)
	}
}
