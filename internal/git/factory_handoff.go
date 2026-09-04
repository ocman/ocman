package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ValidateFactoryHandoff returns the clean shared branch HEAD after confirming it is pushed.
func ValidateFactoryHandoff(ctx context.Context, repoRoot, branch string) (string, error) {
	worktrees, err := ListWorktrees(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	for _, worktree := range worktrees {
		if worktree.Branch != branch {
			continue
		}
		out, err := gitexec.Output(ctx, worktree.Path, "status", "--porcelain", "--untracked-files=normal")
		if err != nil {
			return "", fmt.Errorf("validate Factory handoff: %w", err)
		}
		if len(out) != 0 {
			return "", errors.New("factory worktree has uncommitted changes")
		}
		head, err := gitexec.Output(ctx, worktree.Path, "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("read Factory handoff HEAD: %w", err)
		}
		upstream, err := gitexec.Output(ctx, worktree.Path, "rev-parse", "@{upstream}")
		if err != nil {
			return "", errors.New("factory branch has not been pushed with an upstream")
		}
		headSHA, upstreamSHA := strings.TrimSpace(head), strings.TrimSpace(upstream)
		if headSHA == "" || headSHA != upstreamSHA {
			return "", errors.New("factory branch HEAD has not been pushed")
		}
		return headSHA, nil
	}
	return "", errors.New("factory shared branch worktree was not found")
}
