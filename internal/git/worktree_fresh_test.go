package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/gitexec"
)

func freshGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitexec.Output(t.Context(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWorktreeFreshBase(t *testing.T) {
	for _, base := range []string{"", "refs/remotes/origin/main", "refs/heads/main", "HEAD"} {
		t.Run("base="+base, func(t *testing.T) {
			upstream := initTestRepo(t)
			repo := filepath.Join(t.TempDir(), "checkout")
			gitRun(t, upstream, "clone", upstream, repo)
			repo, _ = filepath.EvalSymlinks(repo)
			old := freshGitOut(t, repo, "rev-parse", "HEAD")
			gitRun(t, upstream, "commit", "--allow-empty", "-m", "new upstream commit")
			want := freshGitOut(t, upstream, "rev-parse", "HEAD")
			if base == "refs/heads/main" || base == "HEAD" {
				want = old
			}
			res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "fresh", NewBranch: true, BaseRef: base})
			if err != nil {
				t.Fatal(err)
			}
			if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got != want {
				t.Fatalf("worktree HEAD = %s, want %s", got, want)
			}
			if got := freshGitOut(t, repo, "rev-parse", "HEAD"); got != old {
				t.Fatal("source checkout changed")
			}
		})
	}
}

func TestWorktreeFetchFailureAndReuse(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	repo, _ = filepath.EvalSymlinks(repo)
	req := CreateWorktreeRequest{RepoRoot: repo, Branch: "fresh", NewBranch: true}
	res, err := CreateWorktree(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	if reused, err := CreateWorktree(t.Context(), req); err != nil || !reused.Reused {
		t.Fatalf("reuse should not fetch: %+v, %v", reused, err)
	}
	req.Branch = "must-not-exist"
	if _, err := CreateWorktree(t.Context(), req); err == nil || !strings.Contains(err.Error(), "refresh worktree base") {
		t.Fatalf("expected visible refresh failure, got %v", err)
	}
	if _, err := os.Stat(WorktreePathFor(repo, req.Branch)); !os.IsNotExist(err) {
		t.Fatalf("failed fetch created worktree: %v", err)
	}
	if branchExists(t.Context(), repo, req.Branch) {
		t.Fatal("failed fetch created branch")
	}
	if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got == "" {
		t.Fatal("existing worktree lost its commit")
	}
}

func TestWorktreeRemoteDefaultChanged(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, upstream, "checkout", "-b", "trunk")
	gitRun(t, upstream, "commit", "--allow-empty", "-m", "new default")
	gitRun(t, repo, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "fresh", NewBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := freshGitOut(t, res.Path, "rev-parse", "HEAD"), freshGitOut(t, upstream, "rev-parse", "HEAD"); got != want {
		t.Fatalf("HEAD = %s, want new remote default %s", got, want)
	}
}

func TestWorktreeExplicitRemoteFailure(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	for _, base := range []string{"refs/remotes/origin/main", "refs/remotes/missing/main"} {
		if _, err := freshWorktreeBase(t.Context(), repo, base); err == nil {
			t.Fatalf("expected refresh failure for %s", base)
		}
	}
	// A local choice remains usable offline, including an existing branch that
	// the form originally requested as new.
	gitRun(t, repo, "branch", "existing")
	if _, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "existing", NewBranch: true}); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeUnnamedRemoteDefault(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, upstream, "checkout", "--detach")
	if _, err := freshWorktreeBase(t.Context(), repo, ""); err == nil || !strings.Contains(err.Error(), "select an explicit base") {
		t.Fatalf("unnamed default = %v", err)
	}
}

func TestWorktreeNonOriginRemote(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, repo, "remote", "rename", "origin", "upstream")
	gitRun(t, upstream, "commit", "--allow-empty", "-m", "new commit")
	for _, base := range []string{"", "refs/remotes/upstream/main"} {
		got, err := freshWorktreeBase(t.Context(), repo, base)
		if want := freshGitOut(t, upstream, "rev-parse", "HEAD"); err != nil || got != want {
			t.Fatalf("base %q = %q, %v; want %s", base, got, err, want)
		}
	}
	gitRun(t, repo, "remote", "add", "other", upstream)
	if _, err := freshWorktreeBase(t.Context(), repo, ""); err == nil || !strings.Contains(err.Error(), "multiple remotes") {
		t.Fatalf("ambiguous default = %v", err)
	}
}
