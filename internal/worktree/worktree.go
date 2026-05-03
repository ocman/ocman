package worktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Errors surfaced by Create. Handlers translate these into specific
// HTTP status codes; the UI uses the message text as a fallback.
var (
	// ErrBranchCheckedOutElsewhere indicates that the branch is
	// already attached to another worktree (git refuses to attach
	// the same branch in two places).
	ErrBranchCheckedOutElsewhere = errors.New("branch is already checked out in another worktree")

	// ErrPathConflict indicates that the target path already exists
	// and is *not* a worktree for the requested branch (e.g. a stale
	// directory or a worktree for a different branch).
	ErrPathConflict = errors.New("worktree path already exists for a different branch")

	// ErrNotARepo indicates the input directory isn't inside a git
	// repository.
	ErrNotARepo = errors.New("directory is not a git repository")
)

// PathFor returns the deterministic on-disk path for a worktree:
//
//	<repo-parent>/.worktrees/<repo-name>/<slug>
//
// The slug is computed via SlugForBranch — the branch name itself is
// kept intact for git invocations.
func PathFor(repoRoot, branch string) string {
	clean := filepath.Clean(repoRoot)
	parent := filepath.Dir(clean)
	repoName := filepath.Base(clean)
	return filepath.Join(parent, ".worktrees", repoName, SlugForBranch(branch))
}

// ResolveRepoRoot returns the top-level directory of the worktree
// containing dir, by shelling out to `git -C <dir> rev-parse
// --show-toplevel`. Returns ErrNotARepo when dir is not inside any
// worktree.
func ResolveRepoRoot(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", ErrNotARepo
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		// git exits non-zero outside a repo. Distinguish "not a
		// repo" from other failures by checking the stderr
		// fragment, but err on the side of ErrNotARepo for any
		// rev-parse failure — handlers turn that into a 404, which
		// is the right shape regardless of the underlying cause.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", ErrNotARepo, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
