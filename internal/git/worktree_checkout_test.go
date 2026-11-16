package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/testutil"
)

func installCheckoutDelay(t *testing.T, afterCheckout string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Keep the real checkout and its output. Model slow checkout finalization
	// without spawning a child that could retain the command's output pipes.
	script := fmt.Sprintf(`#!/bin/sh
if [ "$3" = worktree ] && [ "$4" = add ]; then
  %q "$@" || exit "$?"
  %s
fi
exec %q "$@"
`, realGit, afterCheckout, realGit)
	testutil.WriteScript(t, filepath.Join(bin, "git"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCreateWorktreeSlowCheckout(t *testing.T) {
	repo := initTestRepo(t)
	want := freshGitOut(t, repo, "rev-parse", "HEAD")
	installCheckoutDelay(t, "exec sleep 16")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := CreateWorktree(ctx, CreateWorktreeRequest{
		RepoRoot: repo, Branch: "slow-checkout", NewBranch: true,
	})
	if err != nil {
		t.Fatalf("slow checkout must finish instead of failing session setup: %v", err)
	}
	if res.Reused || res.BranchExisted || freshGitOut(t, res.Path, "rev-parse", "HEAD") != want {
		t.Fatalf("unexpected checkout: %+v", res)
	}
}

func TestCreateWorktreeCheckoutCallerCancellation(t *testing.T) {
	repo := initTestRepo(t)
	marker := filepath.Join(t.TempDir(), "checkout-finished")
	installCheckoutDelay(t, fmt.Sprintf("touch %q\n  exec sleep 30", marker))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := CreateWorktree(ctx, CreateWorktreeRequest{
			RepoRoot: repo, Branch: "cancel-checkout", NewBranch: true,
		})
		done <- err
	}()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("checkout ended before cancellation: %v", err)
		case <-deadline:
			t.Fatal("checkout did not reach finalization")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("finished files must not hide caller cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("checkout ignored caller cancellation")
	}
}

func TestCreateWorktreeCheckoutFinalizationFailure(t *testing.T) {
	repo := initTestRepo(t)
	installCheckoutDelay(t, "echo finalization-failed >&2\n  exit 1")
	res, err := CreateWorktree(t.Context(), CreateWorktreeRequest{
		RepoRoot: repo, Branch: "failed-checkout", NewBranch: true,
	})
	if err == nil || res != nil {
		t.Fatalf("checked-out files must not turn a Git failure into success: result=%+v err=%v", res, err)
	}
}
