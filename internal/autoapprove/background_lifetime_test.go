package autoapprove

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// blockingRules holds the pipeline in its first adapter read until its
// context ends.
type blockingRules struct {
	fakePlatform
	entered  chan struct{}
	calls    atomic.Int32
	returned atomic.Bool
}

func (b *blockingRules) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{PermissionRules: true}
}

func (b *blockingRules) PermissionRules(ctx context.Context, _ string) ([]platforms.PermissionRule, error) {
	if b.calls.Add(1) == 1 {
		close(b.entered)
	}
	<-ctx.Done()
	b.returned.Store(true)
	return nil, ctx.Err()
}

// A judge never outlives the module: shutdown cancels it, Run joins it, and
// nothing is judged after.
func TestModuleRunJoinsBackgroundJudges(t *testing.T) {
	m := newTestModule(nil)
	m.OpenCode = func() bool { return false }
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	go func() { m.Run(ctx); close(ran) }()
	adapter := &blockingRules{entered: make(chan struct{})}
	m.Service().Ensure("fake", adapter, "ses", "per", "bash", []string{"ls"}, nil)
	<-adapter.entered
	cancel()
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
	if !adapter.returned.Load() {
		t.Fatal("Run returned while a judge still ran")
	}
	m.Service().Ensure("fake", adapter, "ses", "per2", "bash", []string{"ls"}, nil)
	time.Sleep(50 * time.Millisecond)
	if n := adapter.calls.Load(); n != 1 {
		t.Fatalf("judged after shutdown: %d adapter reads", n)
	}
}
