package local

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/worktreecleanup"
)

type blockedStartInventory struct{ platforms.Platform }

func (*blockedStartInventory) WorktreeSessions(ctx context.Context) ([]db.Session, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWarmWorktreeStartDoesNotReadHistory(t *testing.T) {
	repo := initRepo(t)
	store, err := state.Open(statetest.Path(t), worktreecleanup.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cleanup := worktreecleanup.New(worktreecleanup.Deps{
		Store: store,
		Platforms: func() []platforms.Platform {
			return []platforms.Platform{&blockedStartInventory{Platform: &platformtest.Fake{PlatformID: "opencode"}}}
		},
	})
	runtime := &fakeRuntime{}
	h := New(Deps{
		Runtime: runtime, WorktreeActivity: cleanup.BeginDirectory,
		CreateSession: func(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "created"}, nil
		},
	})
	t.Cleanup(h.Finalize)
	if _, err := h.EnsureRuntime(t.Context(), "opencode", repo); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	result, err := h.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
		ProjectDir: repo, Branch: "latency", NewBranch: true, BaseRef: "main",
	})
	elapsed := time.Since(started)
	if err != nil || result == nil || elapsed >= time.Second {
		t.Fatalf("warm start exceeded one second: %s result=%+v err=%v", elapsed, result, err)
	}
	if runtime.launchCount() != 1 {
		t.Fatal("warm start relaunched the runtime")
	}
	t.Logf("warm worktree setup with real Git and fake native API: %s", elapsed)
}
