package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeRefreshFallback(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		commands   [][]string
		failFetch  bool
	}{
		{"multiple remotes without origin", "", [][]string{{"remote", "rename", "origin", "upstream"}, {"remote", "add", "other", "unused"}}, false},
		{"unreachable remote default", "", [][]string{{"remote", "set-url", "origin", "missing"}}, false},
		{"unnamed remote default", "", nil, false},
		{"failed default fetch", "", nil, true},
		{"failed explicit fetch", "refs/remotes/origin/main", nil, true},
		{"cached base without configured remote", "refs/remotes/cached/main", [][]string{{"update-ref", "refs/remotes/cached/main", "HEAD"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := initTestRepo(t)
			repo := filepath.Join(t.TempDir(), "checkout")
			gitRun(t, upstream, "clone", upstream, repo)
			repo, _ = filepath.EvalSymlinks(repo)
			want := freshGitOut(t, repo, "rev-parse", "HEAD")
			for _, args := range tc.commands {
				gitRun(t, repo, args...)
			}
			// A selected cached base must not silently become the current branch.
			if tc.base != "" {
				gitRun(t, repo, "checkout", "-b", "local-work")
				gitRun(t, repo, "commit", "--allow-empty", "-m", "local work")
			}
			sourceHead := freshGitOut(t, repo, "rev-parse", "HEAD")
			if tc.name == "unnamed remote default" {
				gitRun(t, upstream, "checkout", "--detach")
			}
			if tc.failFetch {
				realGit, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				bin := t.TempDir()
				script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = fetch ]; then exit 128; fi\nexec %q \"$@\"\n", realGit)
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "fallback", NewBranch: true, BaseRef: tc.base})
			if err != nil {
				t.Fatalf("refresh failure must not block worktree creation: %v", err)
			}
			if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got != want {
				t.Fatalf("worktree HEAD = %s, want cached commit %s", got, want)
			}
			if got := freshGitOut(t, repo, "rev-parse", "HEAD"); got != sourceHead {
				t.Fatal("fallback changed source checkout")
			}
		})
	}
}

func TestWorktreeRefreshFallbackRequiresExplicitBase(t *testing.T) {
	for _, base := range []string{"refs/remotes/origin/missing", "refs/remotes/missing/main"} {
		t.Run(base, func(t *testing.T) {
			repo := initTestRepo(t)
			gitRun(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing"))
			if _, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "missing", NewBranch: true, BaseRef: base}); err == nil {
				t.Fatal("missing explicit base must not fall back to a different branch")
			}
			if branchExists(t.Context(), repo, "missing") {
				t.Fatal("missing explicit base created a branch")
			}
			if _, err := os.Stat(WorktreePathFor(repo, "missing")); !os.IsNotExist(err) {
				t.Fatalf("missing explicit base created worktree: %v", err)
			}
		})
	}
}
