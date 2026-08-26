package git

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// FetchPRHead fetches the standard GitHub/Forgejo pull ref into a stable local branch.
func FetchPRHead(ctx context.Context, repoRoot, remoteName string, prNumber int) (string, error) {
	if repoRoot == "" || remoteName == "" || prNumber <= 0 {
		return "", fmt.Errorf("fetch PR head requires repoRoot, remoteName, prNumber > 0")
	}
	branch := fmt.Sprintf("ocman/pr-%d", prNumber)
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", prNumber, branch)
	err := gitexec.Command(ctx, "-C", repoRoot, "fetch", "--", remoteName, refspec).Run()
	if err != nil {
		return "", fmt.Errorf("git fetch PR head: %w", err)
	}
	return branch, nil
}
