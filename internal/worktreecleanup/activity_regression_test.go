package worktreecleanup

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestNestedDirectoryActivityRenewsRootAndSiblingSessions(t *testing.T) {
	f := newFixture(t)
	f.p.SessionList = append(f.p.SessionList, db.Session{ID: "sibling", Directory: testPath + "/sibling", TimeUpdated: 100, Status: db.StatusDone})
	f.archive(t, "sibling", f.now.Add(-retention-time.Hour))
	f.p.SessionsFn = func(_ context.Context, dir string, _ int64) ([]db.Session, error) {
		var out []db.Session
		for _, s := range f.p.SessionList {
			if dir == "" || s.Directory == dir {
				out = append(out, s)
			}
		}
		return out, nil
	}
	_, release, err := f.m.BeginDirectory(t.Context(), testPath+"/nested")
	if err != nil {
		t.Fatal(err)
	}
	release()
	f.sweep(t, 0)
	for _, id := range []string{"s1", "sibling"} {
		var at int64
		if err := f.store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id = ?`, id).Scan(&at); err != nil || at != f.now.UnixMilli() {
			t.Fatalf("%s grace not renewed: %d %v", id, at, err)
		}
	}
}

type completeActivityInventory struct {
	platforms.Platform
	rows []db.Session
}

func (p *completeActivityInventory) WorktreeSessions(context.Context) ([]db.Session, error) {
	return p.rows, nil
}

func TestNestedDirectoryActivityIncludesHiddenMaintenanceRows(t *testing.T) {
	f := newFixture(t)
	p := &completeActivityInventory{Platform: f.p, rows: append(f.p.SessionList, db.Session{ID: "hidden", ParentID: "s1", Directory: testPath + "/sibling"})}
	f.m.d.Platforms = func() []platforms.Platform { return []platforms.Platform{p} }
	_, release, err := f.m.BeginDirectory(t.Context(), testPath+"/nested")
	if err != nil {
		t.Fatal(err)
	}
	release()
	var at int64
	if err := f.store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id='hidden'`).Scan(&at); err != nil || at != f.now.UnixMilli() {
		t.Fatalf("hidden sibling activity not renewed: %d %v", at, err)
	}
}

func TestFirstNativeObservationPreservesFreshActivity(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.SQL().Exec(`INSERT INTO worktree_cleanup_activity(platform,session_id,activity_at,observed_updated) VALUES('fake','s1',?,0)`, f.now.Add(-retention-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.p.SessionList[0].TimeUpdated = 101
	f.sweep(t, 0)
	var at int64
	if err := f.store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id='s1'`).Scan(&at); err != nil || at != f.now.UnixMilli() {
		t.Fatalf("first native observation discarded: %d %v", at, err)
	}
}
