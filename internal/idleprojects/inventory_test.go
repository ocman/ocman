package idleprojects

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type inventoryPlatform struct {
	*fakePlatform
	reads int
	id    platforms.ID
}

func (p *inventoryPlatform) ID() platforms.ID {
	if p.id != "" {
		return p.id
	}
	return p.fakePlatform.ID()
}

func (p *inventoryPlatform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	p.reads++
	return p.fakePlatform.Sessions(ctx, dir, since)
}

func TestSweepSharesInventoryAcrossRoots(t *testing.T) {
	m, original, runtime := setup(t)
	p := &inventoryPlatform{fakePlatform: original}
	target := m.deps.Targets()[0]
	target.Platform = p
	m.deps.Targets = func() []Target { return []Target{target} }
	runtime.roots = []string{"/repo", "/other", "/third"}
	sweep(t, m, time.Now())
	if p.reads != 1 {
		t.Fatalf("inventory reads = %d, want one per platform/sweep", p.reads)
	}
}

type inventoryRuntime struct {
	*fakeRuntime
	resolutions map[string]int
}

func (r *inventoryRuntime) ProjectRoot(ctx context.Context, directory string) (string, error) {
	r.resolutions[directory]++
	return r.fakeRuntime.ProjectRoot(ctx, directory)
}

func TestSweepInventoryIsPerPlatformAndCachesMembership(t *testing.T) {
	m, original, runtime := setup(t)
	original.sessions = append(original.sessions, db.Session{ID: "child", Directory: "/repo", ParentID: "s", Status: db.StatusDone})
	first := &inventoryPlatform{fakePlatform: original, id: "first"}
	second := &inventoryPlatform{fakePlatform: original, id: "second"}
	r := &inventoryRuntime{fakeRuntime: runtime, resolutions: map[string]int{}}
	r.roots = []string{"/repo", "/other"}
	m.deps.Targets = func() []Target { return []Target{{Platform: first, Runtime: r}, {Platform: second, Runtime: r}} }
	sweep(t, m, time.Now())
	if first.reads != 1 || second.reads != 1 || r.resolutions["/repo"] != 2 {
		t.Fatalf("inventory reads=%d/%d root resolutions=%v", first.reads, second.reads, r.resolutions)
	}
}

func TestSweepFreshInventoryAtStop(t *testing.T) {
	for _, change := range []string{"membership", "new session", "list failure"} {
		t.Run(change, func(t *testing.T) {
			m, p, r := setup(t)
			now := time.Now()
			sweep(t, m, now)
			r.before = func() {
				switch change {
				case "membership":
					r.membership = map[string]string{"/repo": "/elsewhere"}
				case "new session":
					p.sessions = append(p.sessions, db.Session{ID: "new", Directory: "/repo", Status: db.StatusBusy})
				case "list failure":
					p.err = context.Canceled
				}
			}
			sweep(t, m, now.Add(time.Minute))
			if r.stops != 0 || len(m.idle) != 0 {
				t.Fatalf("unsafe cull: stops=%d timers=%v", r.stops, m.idle)
			}
		})
	}
}
