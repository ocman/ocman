package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/gitexec"
)

// CheckCleanupWorktree admits only unchanged, branch-backed checkouts in the
// managed layout. Ignored files are work too; only ignored node_modules trees
// are disposable. No force, git clean, branch deletion or recursive rm is used.
func CheckCleanupWorktree(ctx context.Context, project, path, branch, head string) error {
	if err := managedCleanupPath(project, path); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != filepath.Clean(path) {
		return fmt.Errorf("worktree path is missing or symlinked")
	}
	entries, err := ListWorktrees(ctx, project)
	if err != nil {
		return err
	}
	matched := false
	for _, entry := range entries {
		if filepath.Clean(entry.Path) == filepath.Clean(path) {
			matched = !entry.Main && !entry.Bare && !entry.Locked && branch != "" && head != "" && entry.Branch == branch && entry.Head == head
		}
	}
	if !matched {
		return fmt.Errorf("worktree identity changed, detached, locked or not linked")
	}
	flags, err := cleanupGit(ctx, path, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(flags, "\x00") {
		if entry != "" && (entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z')) {
			return fmt.Errorf("worktree has index flags that can hide tracked edits")
		}
	}
	// Even uninitialized submodules can contain commits or ignored local data.
	modules, err := cleanupGit(ctx, path, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(modules, "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return fmt.Errorf("worktree contains submodules")
		}
	}
	for _, name := range []string{"index.lock", "HEAD.lock", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_LOG"} {
		gitPath, err := cleanupGit(ctx, path, "rev-parse", "--git-path", name)
		if err != nil {
			return err
		}
		p := strings.TrimSpace(gitPath)
		if !filepath.IsAbs(p) {
			p = filepath.Join(path, p)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return fmt.Errorf("worktree has pending Git operation %s", name)
		}
	}
	status, err := cleanupGit(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(status, "\x00") {
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "!! ") && disposableDependency(strings.TrimPrefix(entry, "!! ")) {
			continue
		}
		return fmt.Errorf("%w: tracked, untracked or ignored local files", ErrWorktreeDirty)
	}
	return nil
}

func disposableDependency(path string) bool {
	return strings.HasSuffix(path, "/") && filepath.Base(strings.TrimSuffix(path, "/")) == "node_modules"
}

func managedCleanupPath(project, path string) error {
	if strings.ContainsAny(project+path, "\n\r\t") {
		return fmt.Errorf("cleanup does not support control characters in paths")
	}
	if !filepath.IsAbs(project) || !filepath.IsAbs(path) || project != filepath.Clean(project) || path != filepath.Clean(path) {
		return fmt.Errorf("cleanup requires absolute clean paths")
	}
	main, err := filepath.EvalSymlinks(project)
	if err != nil || main != project {
		return fmt.Errorf("project is missing or symlinked")
	}
	parent := filepath.Dir(WorktreePathFor(project, "placeholder"))
	if filepath.Dir(path) != parent {
		return fmt.Errorf("worktree is outside the managed directory")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return fmt.Errorf("managed worktree directory is missing or symlinked")
	}
	return nil
}

// RestoreCleanupWorktree recreates only a removed checkout on its retained
// branch. Existing linked checkouts are left untouched, including edits made
// after a refused cleanup. Git refuses a branch checked out elsewhere.
func RestoreCleanupWorktree(ctx context.Context, project, path, branch, head string) error {
	if err := managedCleanupPath(project, path); err != nil {
		return err
	}
	if branch == "" || head == "" {
		return fmt.Errorf("missing retained branch identity")
	}
	entries, err := ListWorktrees(ctx, project)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if filepath.Clean(entry.Path) == path {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || resolved != path {
				return fmt.Errorf("checkout is missing or symlinked")
			}
			// A refused or interrupted cleanup may leave a checkout the user
			// has since edited or switched. Never reset it to the old receipt.
			main, err := ResolveMainRepoRoot(ctx, path)
			if err != nil || entry.Main || main != project {
				return fmt.Errorf("existing checkout is not linked to the retained project")
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				return fmt.Errorf("checkout is missing but still registered: %w", err)
			}
			return nil
		}
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("checkout path already exists or cannot be inspected")
	}
	current, err := cleanupGit(ctx, project, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil || strings.TrimSpace(current) != head {
		return fmt.Errorf("retained branch is missing or changed")
	}
	_, err = cleanupGit(ctx, project, "worktree", "add", "--", path, branch)
	return err
}

func cleanupGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, worktreeCommandTimeout)
	defer cancel()
	out, err := gitexec.Command(ctx, append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git cleanup probe: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
