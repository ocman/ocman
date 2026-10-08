package local

import (
	"context"
	"os"

	"github.com/NoUseFreak/ocman/internal/git"
)

// ManagedOpencodeRoot resolves project membership without probing or launching.
// v2's machine-wide membership is handled by the owner's replacement callback.
func (h *Host) ManagedOpencodeRoot(ctx context.Context, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return projectOpencodeRoot(ctx, dir)
}

// projectOpencodeRoot keys a project's managed instance by its main
// checkout, so every linked worktree (inside ocman's .worktrees layout or
// not) shares one instance. A repository without a main checkout (bare
// with linked worktrees) keeps the worktree's own root.
func projectOpencodeRoot(ctx context.Context, dir string) (string, error) {
	main, err := git.ResolveMainRepoRoot(ctx, dir)
	if err != nil {
		return "", err
	}
	if top, err := git.ResolveRepoRoot(ctx, main); err == nil && top == main {
		return main, nil
	}
	return git.ResolveRepoRoot(ctx, dir)
}
