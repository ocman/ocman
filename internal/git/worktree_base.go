package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ResolveBaseRef returns a verified base for new branches: the local default
// branch, its remote-tracking ref, the current branch, then detached HEAD.
// Empty without error means no usable commit is available.
// Named refs are fully qualified so branch/tag collisions cannot change identity.
func ResolveBaseRef(ctx context.Context, repoRoot string) (string, error) {
	remote, err := optionalBaseOutput(ctx, repoRoot, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	if remote != "" {
		for _, ref := range []string{"refs/heads/" + strings.TrimPrefix(remote, "refs/remotes/origin/"), remote} {
			if valid, err := verifyBaseCommit(ctx, repoRoot, ref); err != nil || valid {
				if err != nil {
					return "", err
				}
				return ref, nil
			}
		}
	}
	current, err := optionalBaseOutput(ctx, repoRoot, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", err
	}
	for _, ref := range []string{current, "HEAD"} {
		if ref == "" {
			continue
		}
		valid, err := verifyBaseCommit(ctx, repoRoot, ref)
		if err != nil {
			return "", err
		}
		if valid {
			return ref, nil
		}
	}
	return "", nil
}

func verifyBaseCommit(ctx context.Context, repoRoot, ref string) (bool, error) {
	out, err := optionalBaseOutput(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	return out != "", err
}

// Both quiet probes exit 1 without stderr for an absent/non-symbolic ref.
// Other failures must reach callers, including cancellation and Git diagnostics.
func optionalBaseOutput(ctx context.Context, repoRoot string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	out, err := gitexec.Output(cctx, repoRoot, args...)
	if cctx.Err() != nil {
		return "", cctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve base: git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Factory needs a named delivery branch, while worktrees may start at HEAD.
func resolveFactoryTarget(ctx context.Context, repoRoot string) (string, error) {
	ref, err := ResolveBaseRef(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	if ref != "" && ref != "HEAD" {
		return BaseRefBranch(ref), nil
	}
	for _, branch := range []string{"main", "master"} {
		valid, err := verifyBaseCommit(ctx, repoRoot, "refs/heads/"+branch)
		if err != nil {
			return "", err
		}
		if valid {
			return branch, nil
		}
	}
	return "", errors.New("specify a named Factory target branch; no usable default branch is available")
}
