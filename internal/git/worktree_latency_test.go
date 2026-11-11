package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
)

// Both network stages run before a new worktree session can reach the agent.
// Keep local Git real, but make one network stage take four seconds.
func TestWorktreeSlowRemoteLatency(t *testing.T) {
	for _, stage := range []string{"ls-remote", "fetch"} {
		t.Run(stage, func(t *testing.T) {
			upstream := initTestRepo(t)
			repo := filepath.Join(t.TempDir(), "checkout")
			gitRun(t, upstream, "clone", upstream, repo)
			want := freshGitOut(t, repo, "rev-parse", "HEAD")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = %q ]; then exec sleep 4; fi\nexec %q \"$@\"\n", stage, realGit)
			testutil.WriteScript(t, filepath.Join(bin, "git"), script)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := time.Now()
			res, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "prompt", NewBranch: true})
			elapsed := time.Since(start)
			t.Logf("setup before agent delivery: %s", elapsed)
			if err != nil {
				t.Fatalf("slow optional refresh blocked setup: %v", err)
			}
			if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got != want {
				t.Fatalf("HEAD = %s, want cached %s", got, want)
			}
			if elapsed >= 3*time.Second {
				t.Fatalf("optional remote refresh delayed agent setup by %s, want under 3s", elapsed)
			}
		})
	}
}

func TestWorktreeRemoteRefreshOverlapsNetwork(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, upstream, "commit", "--allow-empty", "-m", "new remote commit")
	want := freshGitOut(t, upstream, "rev-parse", "HEAD")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Discovery cannot finish until fetch starts. Sequential execution stalls
	// and falls back to the old commit; overlapping requests get the new one.
	marker := filepath.Join(bin, "fetch-started")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$3" = ls-remote ]; then
  while [ ! -f %q ]; do sleep 0.01; done
fi
if [ "$3" = fetch ]; then touch %q; fi
exec %q "$@"
`, marker, marker, realGit)
	testutil.WriteScript(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	res, err := CreateWorktree(ctx, CreateWorktreeRequest{RepoRoot: repo, Branch: "overlap", NewBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got != want {
		t.Fatalf("refresh did not overlap: HEAD = %s, want fresh %s", got, want)
	}
}

func TestWorktreeRemoteTransportPipeLatency(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Like git's SSH/credential helper, this child inherits the output pipe.
	// Killing the parent alone must not leave setup waiting for the pipe's EOF.
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = ls-remote ]; then sleep 4 & wait; exit; fi\nexec %q \"$@\"\n", realGit)
	testutil.WriteScript(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	_, err = CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "pipe", NewBranch: true})
	elapsed := time.Since(start)
	t.Logf("setup with inherited transport pipe: %s", elapsed)
	if err != nil || elapsed >= 3*time.Second {
		t.Fatalf("transport pipe delayed setup: %s, %v", elapsed, err)
	}
}

// A controlled RTT makes the normal online improvement measurable without
// contacting a real forge. Correctness is asserted; timings are diagnostic.
func TestWorktreeRemoteRoundTripLatency(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	gitRun(t, upstream, "commit", "--allow-empty", "-m", "new remote commit")
	want := freshGitOut(t, upstream, "rev-parse", "HEAD")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase \"$3\" in ls-remote|fetch) sleep 0.5 ;; esac\nexec %q \"$@\"\n", realGit)
	testutil.WriteScript(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{RepoRoot: repo, Branch: "online", NewBranch: true})
	t.Logf("setup with 500ms per network request: %s", time.Since(start))
	if err != nil {
		t.Fatal(err)
	}
	if got := freshGitOut(t, res.Path, "rev-parse", "HEAD"); got != want {
		t.Fatalf("HEAD = %s, want fresh %s", got, want)
	}
}

func TestWorktreeRefreshCallerCancellation(t *testing.T) {
	upstream := initTestRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	gitRun(t, upstream, "clone", upstream, repo)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := freshWorktreeBase(ctx, repo, ""); err == nil {
		t.Fatal("caller cancellation must not become a cached-base success")
	}
}
