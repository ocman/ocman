package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanGitEnv returns os.Environ() with git context variables stripped.
// Pre-commit hooks (and other git tooling) inject GIT_DIR, GIT_INDEX_FILE,
// GIT_WORK_TREE, etc. which would cause git commands in test subprocesses
// to operate on the wrong repository.
func cleanGitEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, e := range env {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}
		switch key {
		case "GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES":
			// skip
		default:
			out = append(out, e)
		}
	}
	return out
}

func TestPathFor(t *testing.T) {
	tests := []struct {
		name     string
		repoRoot string
		branch   string
		want     string
	}{
		{
			name:     "simple",
			repoRoot: "/home/user/src/myrepo",
			branch:   "main",
			want:     "/home/user/src/.worktrees/myrepo/main",
		},
		{
			name:     "slash-branch",
			repoRoot: "/home/user/src/myrepo",
			branch:   "feature/login",
			want:     "/home/user/src/.worktrees/myrepo/feature-login",
		},
		{
			name:     "trailing-slash-on-repo-root",
			repoRoot: "/home/user/src/myrepo/",
			branch:   "main",
			want:     "/home/user/src/.worktrees/myrepo/main",
		},
		{
			name:     "nested-repo",
			repoRoot: "/a/b/c/d/repo",
			branch:   "x/y/z",
			want:     "/a/b/c/d/.worktrees/repo/x-y-z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PathFor(tt.repoRoot, tt.branch)
			if got != tt.want {
				t.Errorf("PathFor(%q, %q) = %q, want %q",
					tt.repoRoot, tt.branch, got, tt.want)
			}
		})
	}
}

// initTestRepo creates a fresh git repo in a temp directory with one
// commit on `main` and returns the absolute path. The returned dir is
// cleaned up by t.TempDir.
//
// The repo is nested one level inside the temp dir (as "repo/") so
// that the `.worktrees` directory produced by PathFor lands inside the
// test's own isolated temp root rather than the shared OS temp
// directory. Without this nesting, concurrent test packages that all
// use branch names like "feature/login" can collide on the same
// `.worktrees/001/feature-login` path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Nest the repo under "repo/" so .worktrees/ stays inside this test's
	// unique temp root and never collides with other concurrent tests.
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	// Resolve symlinks (macOS /var → /private/var) so later string
	// comparisons against `git rev-parse --show-toplevel` match.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Quiet user.email/name prompts on CI. Strip git context
		// variables so hooks (e.g. pre-commit) don't redirect git
		// commands into the wrong repository.
		cmd.Env = append(cleanGitEnv(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")
	return dir
}

func TestResolveRepoRoot(t *testing.T) {
	repo := initTestRepo(t)

	t.Run("from-repo-root", func(t *testing.T) {
		got, err := ResolveRepoRoot(context.Background(), repo)
		if err != nil {
			t.Fatalf("ResolveRepoRoot(%q): %v", repo, err)
		}
		if got != repo {
			t.Errorf("ResolveRepoRoot(%q) = %q, want %q", repo, got, repo)
		}
	})

	t.Run("from-subdirectory", func(t *testing.T) {
		sub := filepath.Join(repo, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		got, err := ResolveRepoRoot(context.Background(), sub)
		if err != nil {
			t.Fatalf("ResolveRepoRoot(%q): %v", sub, err)
		}
		if got != repo {
			t.Errorf("ResolveRepoRoot(%q) = %q, want %q", sub, got, repo)
		}
	})

	t.Run("non-repo", func(t *testing.T) {
		notRepo := t.TempDir()
		_, err := ResolveRepoRoot(context.Background(), notRepo)
		if err == nil {
			t.Fatalf("ResolveRepoRoot(%q): want error, got nil", notRepo)
		}
		if !strings.Contains(err.Error(), "not a git") {
			// Accept either "not a git repository" or our wrapped
			// ErrNotARepo message.
			t.Logf("error message: %v", err)
		}
	})
}
