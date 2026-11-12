package worktreecleanup

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type slowActivityInventory struct {
	platforms.Platform
	calls int
}

func (p *slowActivityInventory) WorktreeSessions(ctx context.Context) ([]db.Session, error) {
	p.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDirectoryActivityDoesNotWaitForSessionInventory(t *testing.T) {
	f := newFixture(t)
	p := &slowActivityInventory{Platform: f.p}
	f.m.d.Platforms = func() []platforms.Platform { return []platforms.Platform{p} }
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	_, release, err := f.m.BeginDirectory(ctx, testPath)
	if release != nil {
		release()
	}
	if err != nil || p.calls != 0 || time.Since(started) >= time.Second {
		t.Fatalf("directory admission blocked on history: calls=%d duration=%s err=%v", p.calls, time.Since(started), err)
	}
	t.Logf("directory admission without history: %s", time.Since(started))
}
