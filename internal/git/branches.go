package git

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ErrDirtyCheckout is returned by Checkout when git refuses to switch
// branches because the working tree has changes that would be
// overwritten. Handlers map this to HTTP 409 so the UI can surface a
// "commit or stash first" hint rather than a generic failure.
var ErrDirtyCheckout = errors.New("git: checkout would overwrite local changes")

// branchCommandTimeout bounds the branch-list / checkout invocations.
// Checkout can touch many files, so it gets more headroom than a
// read-only status.
const branchCommandTimeout = 10 * time.Second

// ListBranches returns the local branch names for the repository
// containing dir, sorted with the current branch first. dir must be an
// absolute path inside a git worktree. A non-repo directory returns an
// empty slice and no error — the caller treats "no branches" the same
// as "not a repo" for display purposes.
func ListBranches(ctx context.Context, dir string) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, branchCommandTimeout)
	defer cancel()

	// %(refname:short) yields "main", "feature/x", etc. Sorted by most
	// recent commit so the list is roughly usefulness-ordered.
	out, err := gitexec.Output(cctx, dir,
		"for-each-ref", "--format=%(refname:short)",
		"--sort=-committerdate", "refs/heads/")
	if err != nil {
		// Not a repo (or git missing): empty list, no error, matching
		// how Lookup treats non-repos.
		return nil, nil
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		b := strings.TrimSpace(line)
		if b != "" {
			branches = append(branches, b)
		}
	}

	// Float the current branch to the front so the UI can preselect it
	// without a second lookup.
	if cur := Lookup(ctx, dir).Branch; cur != "" && cur != "(detached)" {
		sort.SliceStable(branches, func(i, j int) bool {
			return branches[i] == cur && branches[j] != cur
		})
	}
	return branches, nil
}

// Checkout switches the working tree in dir to branch. It refuses to
// operate on a dirty tree (git's own guard), surfacing ErrDirtyCheckout
// so the caller can distinguish that recoverable case from a real
// failure. The Lookup cache entry for dir is invalidated so the next
// Lookup reflects the new branch immediately.
func Checkout(ctx context.Context, dir, branch string) error {
	cctx, cancel := context.WithTimeout(ctx, branchCommandTimeout)
	defer cancel()

	cmd := gitexec.Command(cctx, "-C", dir, "checkout", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		// git prints this when uncommitted changes would be clobbered.
		if strings.Contains(msg, "would be overwritten") ||
			strings.Contains(msg, "Please commit your changes or stash") {
			return ErrDirtyCheckout
		}
		if strings.TrimSpace(msg) != "" {
			return errors.New(strings.TrimSpace(msg))
		}
		return err
	}
	defaultCache.invalidate(dir)
	return nil
}
