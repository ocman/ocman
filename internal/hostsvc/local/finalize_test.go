package local

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

// finalizeWithin fails the test when Finalize has not returned within d.
func finalizeWithin(t *testing.T, h *Host, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Finalize()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("Finalize did not return within %s", d)
	}
}

// blockUntilDone blocks detached work until its context ends, reporting
// entry and exit.
func blockUntilDone(ctx context.Context, entered, finished chan struct{}) {
	close(entered)
	defer close(finished)
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
}

// TestFinalizeCancelsAndJoinsDetachedLaunch is a regression test: a shared
// runtime launch outlives its caller on a detached context and nothing
// joined it before the server closed state.db.
func TestFinalizeCancelsAndJoinsDetachedLaunch(t *testing.T) {
	h := New(Deps{})
	entered, finished := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		_, _ = h.sfDoDetached(ctx, "/repo", func(c context.Context, _ string) (*hostsvc.EnsureRuntimeResult, error) {
			blockUntilDone(c, entered, finished)
			return nil, c.Err()
		}, "ensure")
	}()
	<-entered
	cancel() // The caller gives up; the shared launch continues detached.

	finalizeWithin(t, h, 5*time.Second)
	select {
	case <-finished:
	default:
		t.Fatal("Finalize returned while the detached launch still ran")
	}
	called := false
	if _, err := h.sfDoDetached(t.Context(), "/repo", func(context.Context, string) (*hostsvc.EnsureRuntimeResult, error) {
		called = true
		return nil, nil
	}, "ensure"); err == nil || called {
		t.Fatalf("launch after Finalize: err=%v called=%v", err, called)
	}
}

// TestFinalizeCancelsAndJoinsWorktreeNaming is a regression test: automatic
// worktree naming ran detached and was only ever waited on by tests.
func TestFinalizeCancelsAndJoinsWorktreeNaming(t *testing.T) {
	repo := initRepo(t)
	entered, finished := make(chan struct{}), make(chan struct{})
	h := New(Deps{
		Runtimes: map[string]func(context.Context, string) (*hostsvc.EnsureRuntimeResult, error){
			"claudecode": func(_ context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
				return &hostsvc.EnsureRuntimeResult{RepoRoot: dir}, nil
			},
		},
		WorktreeNamers: map[string]func(context.Context, string, string) (string, error){
			"claudecode": func(ctx context.Context, _, _ string) (string, error) {
				blockUntilDone(ctx, entered, finished)
				return "", ctx.Err()
			},
		},
		CreatePlatformSession: func(context.Context, string, platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "s"}, nil
		},
	})
	if _, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{Platform: "claudecode", ProjectDir: repo, AutoName: true, Prompt: "Fix login"}); err != nil {
		t.Fatal(err)
	}
	<-entered

	finalizeWithin(t, h, 5*time.Second)
	select {
	case <-finished:
	default:
		t.Fatal("Finalize returned while worktree naming still ran")
	}
}
