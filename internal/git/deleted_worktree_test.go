package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeletedWorktreeReadsAreNotRepository(t *testing.T) {
	repo := initTestRepo(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "deleted-worktree")
	gitRun(t, repo, "worktree", "add", "-b", "deleted", path)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if _, err := GetDiff(t.Context(), path, DiffOptions{Force: true}); !errors.Is(err, ErrNotRepo) {
		t.Errorf("diff error = %v, want ErrNotRepo", err)
	}
	if _, err := ResolveRepoRoot(t.Context(), path); !errors.Is(err, ErrNotARepo) {
		t.Errorf("root error = %v, want ErrNotARepo", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveRepoRoot(ctx, path); !errors.Is(err, context.Canceled) {
		t.Errorf("root cancellation lost: %v", err)
	}
	// Reads must not prune Git's record or substitute the main checkout.
	worktrees, err := ListWorktrees(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 || worktrees[1].Path != path {
		t.Fatalf("historical worktree registration changed: %+v", worktrees)
	}
}
