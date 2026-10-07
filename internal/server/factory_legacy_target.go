package server

import (
	"context"
	"slices"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// legacyFactoryTarget converts a worktree base ref into a forge target branch,
// preserving local branches whose names genuinely start with origin/.
func legacyFactoryTarget(ctx context.Context, owner hostsvc.Host, repo, target string) (string, error) {
	if !strings.HasPrefix(target, "origin/") {
		return target, nil
	}
	branches, err := owner.GitBranches(ctx, repo)
	if err != nil {
		return "", err
	}
	if slices.Contains(branches, target) {
		return target, nil
	}
	return strings.TrimPrefix(target, "origin/"), nil
}
