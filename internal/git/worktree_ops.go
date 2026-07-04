package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// worktreeCommandTimeout bounds each git invocation. Worktree operations
// touch the filesystem and may be slower than a plain rev-parse, so
// give them more headroom than the 2s status ceiling in info.go.
const worktreeCommandTimeout = 15 * time.Second

// addRetryMax is the number of times CreateWorktree will retry a `git worktree
// add` that fails due to a git index lock held by a concurrent git
// process. The back-off starts at addRetryDelay and doubles each
// attempt (capped at addRetryDelayMax).
const (
	addRetryMax      = 5
	addRetryDelay    = 50 * time.Millisecond
	addRetryDelayMax = 500 * time.Millisecond
)

// Worktree is one row from `git worktree list --porcelain`.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"` // short name; empty for detached
	Head   string `json:"head"`
	Bare   bool   `json:"bare"`
	Locked bool   `json:"locked"`
	Main   bool   `json:"main"` // true for the primary worktree
}

// CreateWorktreeRequest captures the user's choice from the form.
type CreateWorktreeRequest struct {
	// RepoRoot is the absolute path of the main checkout.
	RepoRoot string
	// Branch is the (un-slugified) branch name. Required.
	Branch string
	// NewBranch is true when we should create a new branch off
	// BaseRef. False = check out an existing branch.
	NewBranch bool
	// BaseRef is the base when NewBranch is true. Ignored otherwise.
	BaseRef string
}

// CreateWorktreeResult tells the caller what happened.
type CreateWorktreeResult struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Reused bool   `json:"reused"` // true when the worktree already existed for the same branch
	// BranchExisted is true when the caller asked to create a new
	// branch (NewBranch=true) but a branch with that name already
	// existed locally, so we fell back to checking it out instead
	// of creating it. The caller (and ultimately the UI) should
	// warn the user that they're working on a pre-existing branch
	// rather than a freshly-cut one.
	BranchExisted bool `json:"branchExisted"`
}

// ListWorktrees runs `git worktree list --porcelain` and returns the parsed
// entries. The first entry is always the main checkout (Main=true).
func ListWorktrees(ctx context.Context, repoRoot string) ([]Worktree, error) {
	cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	cmd := gitexec.Command(cctx, "-C", repoRoot, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	return parseWorktreeList(string(out))
}

// parseWorktreeList parses the porcelain output of `git worktree list`.
// The format is line-oriented with blank-line-separated stanzas:
//
//	worktree <path>
//	HEAD <sha>
//	branch refs/heads/<short>          (or `bare` or `detached`)
//	locked                              (optional)
//
// We translate `refs/heads/foo` to the short form `foo`.
func parseWorktreeList(in string) ([]Worktree, error) {
	var entries []Worktree
	var cur Worktree
	var open bool
	flush := func() {
		if open {
			entries = append(entries, cur)
		}
		cur = Worktree{}
		open = false
	}

	for _, line := range strings.Split(in, "\n") {
		if line == "" {
			flush()
			continue
		}
		open = true
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			// Leave Branch empty.
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.Locked = true
		}
	}
	flush()

	if len(entries) > 0 {
		entries[0].Main = true
	}
	return entries, nil
}

// CreateWorktree runs `git worktree add` with the right flags and handles
// idempotent reuse. The target path is computed via WorktreePathFor.
//
// Behaviour:
//   - If the target path already exists and `git worktree list`
//     reports a worktree there with the requested branch, return
//     Reused=true without invoking git.
//   - If the target path exists but is not the right worktree, return
//     ErrPathConflict.
//   - If the branch is already attached to another worktree, return
//     ErrBranchCheckedOutElsewhere.
func CreateWorktree(ctx context.Context, req CreateWorktreeRequest) (*CreateWorktreeResult, error) {
	if req.RepoRoot == "" || req.Branch == "" {
		return nil, fmt.Errorf("worktree: RepoRoot and Branch are required")
	}

	target := WorktreePathFor(req.RepoRoot, req.Branch)

	// Idempotency check: is there already a worktree at `target` for
	// the requested branch?
	existing, err := ListWorktrees(ctx, req.RepoRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if filepath.Clean(e.Path) == filepath.Clean(target) {
			if e.Branch == req.Branch {
				return &CreateWorktreeResult{
					Path:   target,
					Branch: req.Branch,
					Reused: true,
				}, nil
			}
			return nil, fmt.Errorf("%w: %s -> %s", ErrPathConflict, target, e.Branch)
		}
		// If the user asked to attach an existing branch and that
		// branch is already in another worktree, fail fast — this
		// matches what git itself would do, but we surface the
		// typed error before the shell-out for cleaner UX.
		if !req.NewBranch && e.Branch == req.Branch {
			return nil, fmt.Errorf("%w: %s at %s",
				ErrBranchCheckedOutElsewhere, req.Branch, e.Path)
		}
	}

	// If `target` exists on disk but git doesn't know about it (a
	// stale dir from a previous `git worktree remove --force` or
	// manual rm), refuse rather than overwrite.
	if _, statErr := os.Stat(target); statErr == nil {
		return nil, fmt.Errorf("%w: %s exists but is not a tracked worktree",
			ErrPathConflict, target)
	}

	// Make sure the parent dir (.worktrees/<repo>) exists. git
	// worktree add creates the leaf, but not arbitrary intermediate
	// directories.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("worktree: mkdir parent: %w", err)
	}

	// If the caller asked to create a new branch but one with that
	// name already exists locally, fall back to a plain checkout
	// rather than failing. The handler surfaces BranchExisted=true
	// so the UI can warn the user that they're working on a
	// pre-existing branch instead of a fresh one. The branch may
	// still be checked out elsewhere; the loop above already caught
	// that, and `git worktree add` will reject it below if some
	// other worktree picked it up between our ListWorktrees() and the add.
	newBranch := req.NewBranch
	branchExisted := false
	if newBranch && branchExists(ctx, req.RepoRoot, req.Branch) {
		newBranch = false
		branchExisted = true
	}

	args := []string{"-C", req.RepoRoot, "worktree", "add"}
	if newBranch {
		args = append(args, "-b", req.Branch, target)
		if req.BaseRef != "" {
			args = append(args, req.BaseRef)
		}
	} else {
		args = append(args, target, req.Branch)
	}

	delay := addRetryDelay
	var addErr error
	for attempt := 0; attempt <= addRetryMax; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			if delay*2 <= addRetryDelayMax {
				delay *= 2
			} else {
				delay = addRetryDelayMax
			}
		}

		cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
		out, err := gitexec.Command(cctx, args...).CombinedOutput()
		cancel()
		if err == nil {
			return &CreateWorktreeResult{
				Path:          target,
				Branch:        req.Branch,
				Reused:        false,
				BranchExisted: branchExisted,
			}, nil
		}
		addErr = classifyAddError(err, string(out))
		if !errors.Is(addErr, ErrIndexLocked) {
			return nil, addErr
		}
	}
	return nil, addErr
}

// RemoveWorktree runs `git worktree remove [--force] <path>` for a worktree in
// repoRoot. git itself enforces the important guards — it refuses the
// main checkout and refuses a dirty tree unless --force is given — so we
// just classify those two failures into typed errors:
//
//   - ErrMainWorktree when path is the primary worktree.
//   - ErrWorktreeDirty when path has uncommitted/untracked changes and
//     force is false (the caller can retry with force=true).
func RemoveWorktree(ctx context.Context, repoRoot, path string, force bool) error {
	if repoRoot == "" || path == "" {
		return fmt.Errorf("worktree: repoRoot and path are required")
	}
	args := []string{"-C", repoRoot, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)

	cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	out, err := gitexec.Command(cctx, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	return classifyRemoveError(err, string(out))
}

// classifyRemoveError maps a `git worktree remove` failure to a typed
// error when the message matches a known pattern.
func classifyRemoveError(err error, output string) error {
	out := strings.ToLower(output)
	switch {
	case strings.Contains(out, "is a main working tree"):
		return fmt.Errorf("%w: %s", ErrMainWorktree, strings.TrimSpace(output))
	case strings.Contains(out, "use --force to delete"),
		strings.Contains(out, "contains modified or untracked files"):
		return fmt.Errorf("%w: %s", ErrWorktreeDirty, strings.TrimSpace(output))
	default:
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(output))
	}
}

// branchExists reports whether a local branch named `branch` exists
// in repoRoot. Errors (including the branch genuinely not existing)
// are coerced to false — callers treat "unknown" the same as "no" and
// let `git worktree add` produce the authoritative error if any.
func branchExists(ctx context.Context, repoRoot, branch string) bool {
	cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	return gitexec.Command(cctx, "-C", repoRoot,
		"show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// classifyAddError translates a `git worktree add` failure into one of
// our typed errors when the message matches a known pattern. Falls
// back to a wrapped error preserving git's own output.
func classifyAddError(err error, output string) error {
	out := strings.ToLower(output)
	switch {
	case strings.Contains(out, "is already used by worktree"),
		strings.Contains(out, "already checked out"):
		return fmt.Errorf("%w: %s", ErrBranchCheckedOutElsewhere, strings.TrimSpace(output))
	case strings.Contains(out, "already exists"):
		return fmt.Errorf("%w: %s", ErrPathConflict, strings.TrimSpace(output))
	case strings.Contains(out, "index.lock"),
		strings.Contains(out, "index file open failed"):
		return fmt.Errorf("%w: %s", ErrIndexLocked, strings.TrimSpace(output))
	default:
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(output))
	}
}

// ResolveBaseRef returns the best-guess base ref for new branches in
// the given repo (AD-5):
//
//  1. origin/HEAD's target (the upstream's default branch).
//  2. The repo's currently checked-out branch.
//  3. Literal "main".
//
// Errors at any step fall through to the next option silently — the
// returned value is *always* a usable string. Used to pre-fill the
// "base ref" field in the worktree-creation form.
func ResolveBaseRef(ctx context.Context, repoRoot string) string {
	// (1) origin/HEAD
	if ref := runGitOutput(ctx, repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); ref != "" {
		// Output looks like "origin/main"; strip the remote prefix.
		if idx := strings.IndexByte(ref, '/'); idx >= 0 {
			return ref[idx+1:]
		}
		return ref
	}
	// (2) current branch
	if ref := runGitOutput(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); ref != "" && ref != "HEAD" {
		return ref
	}
	// (3) sentinel
	return "main"
}

// runGitOutput is a small helper around exec.CommandContext that
// returns trimmed stdout on success and an empty string on any error.
func runGitOutput(ctx context.Context, repoRoot string, args ...string) string {
	cctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	out, err := gitexec.Output(cctx, repoRoot, args...)
	if err != nil {
		return ""
	}
	return out
}
