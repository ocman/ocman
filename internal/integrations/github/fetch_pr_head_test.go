package github

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// TestFetchPRHead_FetchesAndCreatesLocalBranch verifies the
// canonical workflow: an upstream remote has a refs/pull/N/head ref
// pointing at a commit not in our local checkout, and FetchPRHead
// brings it down into a deterministic local branch `ocman/pr-N`.
//
// The "upstream" remote is a bare repo on disk, populated with a
// pull/N/head ref via `git update-ref`. This is exactly how GitHub /
// Forgejo expose PR refs.
func TestFetchPRHead_FetchesAndCreatesLocalBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	upstream := filepath.Join(root, "upstream.git")
	local := filepath.Join(root, "local")

	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitexec.CleanEnv(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Build a "source" repo, make two commits on a feature branch,
	// then push to a bare "upstream" so we can simulate the PR head ref.
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(source, "init", "-b", "main")
	runGit(source, "config", "user.email", "test@example.com")
	runGit(source, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(source, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(source, "add", "a")
	runGit(source, "commit", "-m", "initial")
	runGit(source, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(source, "b"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(source, "add", "b")
	runGit(source, "commit", "-m", "feature commit")

	// Bare upstream repo.
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(upstream, "init", "--bare", "-b", "main")

	// Push main + feature to upstream.
	runGit(source, "remote", "add", "origin", upstream)
	runGit(source, "push", "origin", "main:main")
	runGit(source, "push", "origin", "feature:feature")

	// Create the simulated pull/7/head ref pointing at the feature tip.
	headSha := func() string {
		cmd := exec.Command("git", "rev-parse", "feature")
		cmd.Dir = source
		cmd.Env = gitexec.CleanEnv()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		return string(out[:len(out)-1])
	}()
	runGit(upstream, "update-ref", "refs/pull/7/head", headSha)

	// Now clone the upstream as a non-bare local repo with origin = upstream.
	runGit(root, "clone", upstream, "local")

	// Sanity: local does NOT yet have an "ocman/pr-7" branch.
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/ocman/pr-7")
	cmd.Dir = local
	cmd.Env = gitexec.CleanEnv()
	if err := cmd.Run(); err == nil {
		t.Fatal("pre-condition failed: ocman/pr-7 already exists locally")
	}

	c := &Client{token: ""}
	branch, err := c.FetchPRHead(context.Background(), local, "origin", 7)
	if err != nil {
		t.Fatalf("FetchPRHead: %v", err)
	}
	if branch != "ocman/pr-7" {
		t.Errorf("branch name: got %q want ocman/pr-7", branch)
	}

	// Verify the branch was created and points at the same SHA.
	cmd = exec.Command("git", "rev-parse", "refs/heads/ocman/pr-7")
	cmd.Dir = local
	cmd.Env = gitexec.CleanEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse ocman/pr-7: %v", err)
	}
	if got := string(out[:len(out)-1]); got != headSha {
		t.Errorf("branch points at %s, want %s", got, headSha)
	}

	// Re-running is idempotent — should not error.
	if _, err := c.FetchPRHead(context.Background(), local, "origin", 7); err != nil {
		t.Errorf("second FetchPRHead: %v", err)
	}
}
