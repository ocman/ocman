package github

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// FetchPRHead fetches the PR's head ref from the given remote into a
// deterministic local branch named "ocman/pr-<n>". Used by FR-9a
// (cross-fork worktree launches) so we have a real local branch the
// worktree creator can attach.
//
// Idempotent: re-running the fetch updates the existing branch in
// place (force-update via the leading '+' in the refspec). The branch
// name is stable across runs so subsequent worktree launches reuse
// the same branch.
//
// Implements forge.Forge.FetchPRHead. GitHub and Forgejo use the same
// "refs/pull/<n>/head" naming for PR heads, so the only forge-specific
// part is the remote name (which the caller supplies).
func (c *Client) FetchPRHead(ctx context.Context, repoRoot, remoteName string, prNumber int) (string, error) {
	if repoRoot == "" || remoteName == "" || prNumber <= 0 {
		return "", fmt.Errorf("github: FetchPRHead requires repoRoot, remoteName, prNumber > 0")
	}
	branch := fmt.Sprintf("ocman/pr-%d", prNumber)
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", prNumber, branch)

	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "fetch", remoteName, refspec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git fetch %s %s: %w: %s",
			remoteName, refspec, err, strings.TrimSpace(string(out)))
	}
	return branch, nil
}
