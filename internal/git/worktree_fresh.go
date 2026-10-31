package git

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/gitexec"
)

const worktreeFetchTimeout = 60 * time.Second

// freshWorktreeBase refreshes remote bases without moving any local branch.
// Explicit local refs and commits are deliberate snapshots and stay unchanged.
func freshWorktreeBase(ctx context.Context, repo, base string) (string, error) {
	if base != "" && !strings.HasPrefix(base, "refs/remotes/") {
		return base, nil
	}
	remotes, err := optionalBaseOutput(ctx, repo, "remote")
	if err != nil {
		return "", err
	}
	remote := ""
	names := strings.Fields(remotes)
	for _, name := range names {
		if base == "" && name == "origin" || strings.HasPrefix(base, "refs/remotes/"+name+"/") && len(name) > len(remote) {
			remote = name
		}
	}
	if base == "" && remote == "" && len(names) == 1 {
		remote = names[0]
	}
	if remote == "" {
		if base != "" {
			return "", fmt.Errorf("refresh worktree base: no configured remote for %s", base)
		}
		if len(names) > 0 {
			return "", fmt.Errorf("refresh worktree base: multiple remotes without origin; select an explicit base")
		}
		return ResolveBaseRef(ctx, repo)
	}
	cctx, cancel := context.WithTimeout(ctx, worktreeFetchTimeout)
	defer cancel()
	if base == "" {
		out, err := gitexec.Output(cctx, repo, "ls-remote", "--symref", "--", remote, "HEAD")
		if err != nil {
			return "", fmt.Errorf("refresh worktree base: discover %s default: %w", remote, err)
		}
		for _, line := range strings.Split(out, "\n") {
			if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok && strings.HasSuffix(ref, "\tHEAD") {
				base = "refs/remotes/" + remote + "/" + strings.TrimSuffix(ref, "\tHEAD")
				break
			}
		}
		if base == "" {
			return "", fmt.Errorf("refresh worktree base: %s has no named default branch; select an explicit base", remote)
		}
	}
	branch := strings.TrimPrefix(base, "refs/remotes/"+remote+"/")
	out, err := gitexec.Command(cctx, "-C", repo, "fetch", "--no-tags", "--", remote, "+refs/heads/"+branch+":"+base).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("refresh worktree base: fetch %s: %w: %s", base, err, strings.TrimSpace(string(out)))
	}
	// Pin this creation to the fetched commit, even if another fetch updates refs.
	commit, err := optionalBaseOutput(ctx, repo, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	return commit, err
}
