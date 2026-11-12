package worktreecleanup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestActivityAdmissionFailsClosedWithoutRecoveryMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     func(*testing.T, *fixture)
		directory bool
	}{
		{"activity store unavailable", func(t *testing.T, f *fixture) {
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"checkpoint query unavailable", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_checkout") }, false},
		{"session metadata missing", func(t *testing.T, f *fixture) {
			rememberFixture(t, f)
			f.p.SessionFn = func(string) (*platforms.SessionDetail, error) { return nil, nil }
		}, false},
		{"session metadata read fails", func(t *testing.T, f *fixture) {
			rememberFixture(t, f)
			f.p.SessionFn = func(string) (*platforms.SessionDetail, error) { return nil, errors.New("history unavailable") }
		}, false},
		{"directory checkpoint unavailable", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_checkout") }, true},
		{"directory activity write fails", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_directory_activity") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			var err error
			if tc.directory {
				_, _, err = f.m.BeginDirectory(t.Context(), testPath)
			} else {
				_, _, err = f.m.BeginSession(t.Context(), f.p, "s1")
			}
			if err == nil {
				t.Fatal("admission ignored unavailable safety metadata")
			}
			if len(f.h.restored) != 0 || len(f.h.removed) != 0 {
				t.Fatal("failed admission changed a checkout")
			}
			assertActivityLeaseReleased(t, f)
		})
	}
}

func execFixtureSQL(t *testing.T, f *fixture, query string) {
	t.Helper()
	if _, err := f.store.SQL().ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}

func rememberFixture(t *testing.T, f *fixture) {
	t.Helper()
	if err := f.m.remember(t.Context(), "local", hostsvc.WorktreeCheckpoint{Project: "/projects/repo", Path: testPath, Branch: "retained", Head: "abc"}); err != nil {
		t.Fatal(err)
	}
}

func assertActivityLeaseReleased(t *testing.T, f *fixture) {
	t.Helper()
	done := make(chan struct{})
	go func() { _, release := f.store.WorktreeMaintenance(t.Context()); release(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed activity admission leaked its lease")
	}
}

func TestUnavailableAdaptersDoNotBlockUnrelatedDirectoryActivity(t *testing.T) {
	f := newFixture(t)
	f.p.SessionsErr = errors.New("adapter unavailable")
	_, release, err := f.m.BeginDirectory(t.Context(), testPath)
	if err != nil {
		t.Fatal(err)
	}
	release()
	f.p.SessionsErr = nil
	f.p.SessionList[0].Directory = "/unrelated"
	_, release, err = f.m.BeginDirectory(t.Context(), testPath)
	if err != nil {
		t.Fatal(err)
	}
	release()
	var count int
	if err := f.store.SQL().QueryRow(`SELECT COUNT(*) FROM worktree_cleanup_activity`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unrelated activity reset deadlines: %d %v", count, err)
	}
}

type legacyCleanupHost struct{ hostsvc.Host }

func TestRecoveryRetainsReceiptWhenOwnerCannotRestore(t *testing.T) {
	f := newFixture(t)
	rememberFixture(t, f)
	router := hostsvc.NewRouter(&legacyCleanupHost{Host: f.h})
	f.m.d.Router = func() *hostsvc.Router { return router }
	if err := f.m.restore(t.Context(), "local", testPath); err == nil || !strings.Contains(err.Error(), "cannot restore") {
		t.Fatalf("unsupported owner: %v", err)
	}
	if receipt, err := f.m.checkpoint(t.Context(), "local", testPath); err != nil || receipt == nil {
		t.Fatal("failed recovery lost its receipt")
	}
}

type backgroundCleanupPlatform struct {
	*cleanupPlatform
	snapshot *platforms.BackgroundSnapshot
	err      error
}

func (p *backgroundCleanupPlatform) BackgroundTasks(context.Context, string) (*platforms.BackgroundSnapshot, error) {
	return p.snapshot, p.err
}
func (*backgroundCleanupPlatform) SetBackgroundLifecycle(context.Context, platforms.SetBackgroundLifecycleRequest) error {
	return platforms.ErrUnsupported
}

func cleanupRequest() hostsvc.CleanupWorktreeRequest {
	return hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Project: "/projects/repo", Path: testPath, Branch: "retained", Head: "abc"}, Sessions: []hostsvc.WorktreeSessionCheck{{Platform: "fake", SessionID: "s1", TimeUpdated: 100}}}
}

func TestOwnerRejectsUnverifiableBackgroundAndNativeWork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *fixture)
	}{
		{"background task", func(_ *testing.T, f *fixture) {
			p := &backgroundCleanupPlatform{cleanupPlatform: f.p, snapshot: &platforms.BackgroundSnapshot{Count: 1}}
			f.m.d.Platforms = func() []platforms.Platform { return []platforms.Platform{p} }
		}},
		{"background read failed", func(_ *testing.T, f *fixture) {
			p := &backgroundCleanupPlatform{cleanupPlatform: f.p, err: errors.New("cannot inspect background work")}
			f.m.d.Platforms = func() []platforms.Platform { return []platforms.Platform{p} }
		}},
		{"native inventory failed", func(_ *testing.T, f *fixture) {
			f.p.SessionIdentitiesFn = func(context.Context) ([]db.Session, error) { return nil, errors.New("native inventory unavailable") }
		}},
		{"native lifecycle failed", func(_ *testing.T, f *fixture) {
			f.p.SessionLifecycleFn = func(string) (db.SessionLifecycle, error) {
				return db.SessionLifecycle{}, errors.New("native lifecycle unavailable")
			}
		}},
		{"bounded lifecycle unsupported", func(_ *testing.T, f *fixture) {
			f.m.d.Platforms = func() []platforms.Platform { return []platforms.Platform{f.p.Fake} }
		}},
		{"queue read failed", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE queued_message") }},
		{"activity read failed", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_activity") }},
		{"queued work", func(t *testing.T, f *fixture) {
			if err := f.store.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "q", Platform: "fake", SessionID: "s1", Text: "held", ImagesJSON: "[]", CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
		}},
		{"receipt write failed", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE worktree_cleanup_checkout") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			if _, _, err := f.m.AdmitCleanup(t.Context(), cleanupRequest()); err == nil {
				t.Fatal("cleanup admitted unverifiable work")
			}
			assertActivityLeaseReleased(t, f)
		})
	}
}

func TestSchedulingAndRemovalStopOnPersistenceFailure(t *testing.T) {
	for _, table := range []string{"archived_session", "worktree_cleanup_activity", "queued_message", "worktree_cleanup_checkout"} {
		t.Run(table, func(t *testing.T) {
			f := newFixture(t)
			execFixtureSQL(t, f, "DROP TABLE "+table)
			owners, _ := f.m.inventory(t.Context())
			if _, err := f.m.clean(t.Context(), f.h, f.h, "local", testPath, owners["local"]); err == nil {
				t.Fatal("cleanup ignored missing persistence")
			}
			if len(f.h.removed) != 0 {
				t.Fatal("persistence failure removed checkout")
			}
		})
	}
	f := newFixture(t)
	owners, _ := f.m.inventory(t.Context())
	f.h.beforeRemove = func(context.Context, hostsvc.CleanupWorktreeRequest) error {
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		return errors.New("owner refused removal")
	}
	if _, err := f.m.clean(t.Context(), f.h, f.h, "local", testPath, owners["local"]); err == nil {
		t.Fatal("lost outcome persistence failure")
	}
	if len(f.h.removed) != 0 {
		t.Fatal("owner refusal was ignored")
	}
}
