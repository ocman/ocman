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
		upstreamBranch, err := gitexec.Output(ctx, worktree.Path, "config", "--get", "branch."+branch+".merge")
		if err != nil || strings.TrimSpace(upstreamBranch) != "refs/heads/"+branch {
			return "", errors.New("factory branch has not been pushed with an upstream")
		}
		headSHA, upstreamSHA := strings.TrimSpace(head), strings.TrimSpace(upstream)
		if headSHA == "" || headSHA != upstreamSHA {
			return "", errors.New("factory branch HEAD has not been pushed")
		}
		remote, err := gitexec.Output(ctx, worktree.Path, "config", "--get", "branch."+branch+".remote")
		if err != nil || strings.TrimSpace(remote) == "." || strings.TrimSpace(remote) == "" {
			return "", errors.New("factory branch has not been pushed with an upstream")
		}
		published, err := gitexec.Output(ctx, worktree.Path, "ls-remote", "--exit-code", "--", strings.TrimSpace(remote), "refs/heads/"+branch)
		if err != nil {
			return "", fmt.Errorf("verify published Factory branch: %w", err)
		}
		fields := strings.Fields(published)
		if len(fields) != 2 || fields[0] != headSHA {
			return "", errors.New("factory branch HEAD has not been pushed")
		}
		return headSHA, nil
	}
	return "", errors.New("factory shared branch worktree was not found")
}

// PrepareFactoryWorkspace checks the accepted checkpoint before another issue
// touches the branch. A missing branch needs explicit recovery, not a silent reset.
func PrepareFactoryWorkspace(ctx context.Context, repoRoot, branch, checkpoint, target string) (string, string, error) {
	if target == "" {
		target = ResolveBaseRef(ctx, repoRoot)
	}
	if _, err := gitexec.Output(ctx, repoRoot, "check-ref-format", "--branch", branch); err != nil {
		return "", "", errors.New("invalid Factory branch")
	}
	if _, err := gitexec.Output(ctx, repoRoot, "check-ref-format", "--branch", target); err != nil {
		return "", "", errors.New("invalid Factory target branch")
	}
	head, err := gitexec.Output(ctx, repoRoot, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		if checkpoint != "" {
			return "", "", errors.New("factory checkpoint branch is missing; restore it before retrying")
		}
		base := "refs/remotes/origin/" + target
		if _, err := gitexec.Output(ctx, repoRoot, "rev-parse", "--verify", base); err != nil {
			base = "refs/heads/" + target
		}
		if _, err := gitexec.Output(ctx, repoRoot, "rev-parse", "--verify", base); err != nil {
			return "", "", errors.New("factory target branch is unavailable")
		}
		return target, base, nil
	}
	if checkpoint != "" && strings.TrimSpace(head) != checkpoint {
		return "", "", errors.New("factory branch differs from its accepted checkpoint; reconcile it before retrying")
	}
	if checkpoint != "" {
		if _, err := ValidateFactoryHandoff(ctx, repoRoot, branch); err != nil {
			return "", "", err
		}
	}
	return target, "", nil
}

// ValidateFactoryCheckpoint rejects a handoff that discarded accepted work.
func ValidateFactoryCheckpoint(ctx context.Context, repoRoot, branch, checkpoint string) (string, error) {
	head, err := ValidateFactoryHandoff(ctx, repoRoot, branch)
	if err != nil {
		return "", err
	}
	if checkpoint != "" {
		if _, err := gitexec.Output(ctx, repoRoot, "merge-base", "--is-ancestor", checkpoint, head); err != nil {
			return "", errors.New("factory handoff does not include the accepted checkpoint")
		}
	}
	return head, nil
}
