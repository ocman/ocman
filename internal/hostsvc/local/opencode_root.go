package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/git"
)

// projectOpencodeRoot resolves a project's root by its main checkout, so
// every linked worktree (inside ocman's .worktrees layout or not) reports
// the same project. A repository without a main checkout (bare
// with linked worktrees) keeps the worktree's own root. Plain project
// directories use their canonical path; launching does not require Git.
func projectOpencodeRoot(ctx context.Context, dir string) (string, error) {
	main, err := git.ResolveMainRepoRoot(ctx, dir)
	if errors.Is(err, git.ErrNotARepo) && dir != "" {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			return "", statErr
		}
		if !info.IsDir() {
			return "", fmt.Errorf("project path is not a directory: %s", dir)
		}
		root, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(root)
	}
	if err != nil {
		return "", err
	}
	if top, err := git.ResolveRepoRoot(ctx, main); err == nil && top == main {
		return main, nil
	}
	return git.ResolveRepoRoot(ctx, dir)
}
