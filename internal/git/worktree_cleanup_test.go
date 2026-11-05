package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cleanupFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo := initTestRepo(t)
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n.env\n.cache/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-m", "ignore fixtures")
	res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "cleanup-test", NewBranch: true, BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ListWorktrees(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == res.Path {
			return repo, res.Path, entry.Head
		}
	}
	t.Fatal("missing created worktree")
	return "", "", ""
}

func TestCleanupWorktreeGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, string, string)
		allow  bool
	}{
		{"clean", nil, true},
		{"ignored dependencies", func(t *testing.T, _, path string) { writeCleanupFile(t, path, "node_modules/pkg/index.js") }, true},
		{"nested ignored dependencies", func(t *testing.T, _, path string) { writeCleanupFile(t, path, "frontend/node_modules/pkg/index.js") }, true},
		{"untracked", func(t *testing.T, _, path string) { writeCleanupFile(t, path, "notes.txt") }, false},
		{"ignored secret", func(t *testing.T, _, path string) { writeCleanupFile(t, path, ".env") }, false},
		{"ignored cache", func(t *testing.T, _, path string) { writeCleanupFile(t, path, ".cache/state.db") }, false},
		{"tracked edit", func(t *testing.T, _, path string) { writeCleanupFile(t, path, ".gitignore") }, false},
		{"staged edit", func(t *testing.T, _, path string) {
			writeCleanupFile(t, path, "staged")
			runGit(t, path, "add", "staged")
		}, false},
		{"locked", func(t *testing.T, repo, path string) { runGit(t, repo, "worktree", "lock", path) }, false},
		{"detached", func(t *testing.T, _, path string) { runGit(t, path, "checkout", "--detach") }, false},
		{"renamed branch", func(t *testing.T, _, path string) { runGit(t, path, "branch", "-m", "changed") }, false},
		{"new commit", func(t *testing.T, _, path string) { runGit(t, path, "commit", "--allow-empty", "-m", "new work") }, false},
		{"submodule", func(t *testing.T, _, path string) {
			runGit(t, path, "update-index", "--add", "--cacheinfo", "160000,"+runGit(t, path, "rev-parse", "HEAD")+",submodule")
		}, false},
		{"index lock", func(t *testing.T, _, path string) {
			p, err := cleanupGit(t.Context(), path, "rev-parse", "--git-path", "index.lock")
			if err != nil {
				t.Fatal(err)
			}
			p = filepath.Clean(p[:len(p)-1])
			if !filepath.IsAbs(p) {
				p = filepath.Join(path, p)
			}
			if err := os.WriteFile(p, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, path, head := cleanupFixture(t)
			if tc.change != nil {
				tc.change(t, repo, path)
			}
			err := CheckCleanupWorktree(t.Context(), repo, path, "cleanup-test", head)
			if (err == nil) != tc.allow {
				t.Fatalf("CheckCleanupWorktree = %v, allow=%v", err, tc.allow)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("guard modified checkout: %v", err)
			}
		})
	}
}

func writeCleanupFile(t *testing.T, path, file string) {
	t.Helper()
	full := filepath.Join(path, file)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("local data"), 0600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	out, err := cleanupGit(t.Context(), path, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func TestCleanupRejectsUnsafePaths(t *testing.T) {
	repo, path, head := cleanupFixture(t)
	alias := filepath.Join(filepath.Dir(repo), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{repo, alias, "relative/path", filepath.Join(filepath.Dir(path), "missing"), path + "/.."} {
		if err := CheckCleanupWorktree(t.Context(), repo, candidate, "cleanup-test", head); err == nil {
			t.Errorf("accepted %s", candidate)
		}
	}
	if err := CheckCleanupWorktree(t.Context(), repo, path, "", head); err == nil {
		t.Fatal("accepted detached identity")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CheckCleanupWorktree(ctx, repo, path, "cleanup-test", head); err == nil {
		t.Fatal("accepted cancelled probe")
	}
}

func TestCleanupAndRestoreKeepsUnpushedCommit(t *testing.T) {
	repo, path, _ := cleanupFixture(t)
	writeCleanupFile(t, path, "committed.txt")
	runGit(t, path, "add", "committed.txt")
	runGit(t, path, "commit", "-m", "valuable unpushed work")
	entries, err := ListWorktrees(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var head string
	for _, entry := range entries {
		if entry.Path == path {
			head = entry.Head
		}
	}
	if err := CheckCleanupWorktree(t.Context(), repo, path, "cleanup-test", head); err != nil {
		t.Fatal(err)
	}
	writeCleanupFile(t, path, "node_modules/pkg/index.js")
	if err := RemoveWorktree(t.Context(), repo, path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "cleanup-test", head); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "committed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "cleanup-test", head); err != nil {
		t.Fatalf("idempotent restore: %v", err)
	}
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "other", head); err != nil {
		t.Fatalf("existing checkout should remain untouched: %v", err)
	}
	runGit(t, path, "commit", "--allow-empty", "-m", "work after refused cleanup")
	newHead := runGit(t, path, "rev-parse", "HEAD")
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "cleanup-test", head); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, path, "rev-parse", "HEAD"); got != newHead {
		t.Fatal("recovery reset existing work")
	}
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "", head); err == nil {
		t.Fatal("restored missing branch identity")
	}
}

func TestRestoreDoesNotOverwriteLocalPathsOrChangedBranches(t *testing.T) {
	repo, path, head := cleanupFixture(t)
	if err := RemoveWorktree(t.Context(), repo, path, false); err != nil {
		t.Fatal(err)
	}
	writeCleanupFile(t, path, "precious.txt")
	if err := RestoreCleanupWorktree(t.Context(), repo, path, "cleanup-test", head); err == nil {
		t.Fatal("overwrote local path")
	}
	other := filepath.Join(filepath.Dir(path), "new-path")
	if err := RestoreCleanupWorktree(t.Context(), repo, other, "cleanup-test", "bad-head"); err == nil {
		t.Fatal("accepted changed branch")
	}
	if err := RestoreCleanupWorktree(t.Context(), "relative", other, "cleanup-test", head); err == nil {
		t.Fatal("accepted unsafe project")
	}
}
