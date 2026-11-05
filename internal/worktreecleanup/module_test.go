package worktreecleanup

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

const testPath = "/projects/.worktrees/repo/session-1"

type cleanupPlatform struct {
	*platformtest.Fake
	lifecycle    *platforms.SessionLifecycle
	lifecycleErr error
	native       []platforms.NativeQueuedMessage
	nativeErr    error
}

func (p *cleanupPlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	if p.lifecycleErr != nil {
		return nil, p.lifecycleErr
	}
	if p.lifecycle != nil {
		return p.lifecycle, nil
	}
	return &platforms.SessionLifecycle{Status: db.StatusDone}, nil
}
func (p *cleanupPlatform) NativeQueued(context.Context, string) ([]platforms.NativeQueuedMessage, error) {
	return p.native, p.nativeErr
}
func (*cleanupPlatform) CancelNativeQueued(context.Context, platforms.CancelNativeQueuedRequest) error {
	return nil
}

type cleanupHost struct {
	hostsvc.Host
	entries                        []git.Worktree
	listErr, removeErr, restoreErr error
	removed                        []hostsvc.CleanupWorktreeRequest
	restored                       []hostsvc.WorktreeCheckpoint
	beforeRemove                   func(context.Context, hostsvc.CleanupWorktreeRequest) error
}

func (*cleanupHost) RemoteID() string { return "local" }
func (h *cleanupHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	return h.entries, h.listErr
}
func (h *cleanupHost) CleanupWorktree(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
	if h.beforeRemove != nil {
		if err := h.beforeRemove(ctx, req); err != nil {
			return err
		}
	}
	if h.removeErr != nil {
		return h.removeErr
	}
	h.removed = append(h.removed, req)
	h.entries = nil
	return nil
}
func (h *cleanupHost) RestoreWorktree(_ context.Context, checkpoint hostsvc.WorktreeCheckpoint) error {
	if h.restoreErr != nil {
		return h.restoreErr
	}
	h.restored = append(h.restored, checkpoint)
	h.entries = []git.Worktree{{Path: checkpoint.Path, Branch: checkpoint.Branch, Head: checkpoint.Head}}
	return nil
}

type fixture struct {
	m     *Module
	store *state.DB
	p     *cleanupPlatform
	h     *cleanupHost
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := state.Open(statetest.Path(t), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fixture{store: store, now: time.Now()}
	f.p = &cleanupPlatform{Fake: &platformtest.Fake{PlatformID: "fake", SessionList: []db.Session{{ID: "s1", Directory: testPath, TimeUpdated: 100, Status: db.StatusDone}}}}
	f.p.SessionFn = func(id string) (*platforms.SessionDetail, error) {
		for _, s := range f.p.SessionList {
			if s.ID == id {
				return &platforms.SessionDetail{Session: &s}, nil
			}
		}
		return nil, platforms.ErrNotFound
	}
	f.h = &cleanupHost{entries: []git.Worktree{{Path: testPath, Branch: "retained", Head: "abc"}}}
	router := hostsvc.NewRouter(f.h)
	f.m = New(Deps{Store: store, Platforms: func() []platforms.Platform { return []platforms.Platform{f.p} }, Router: func() *hostsvc.Router { return router }, Now: func() time.Time { return f.now }})
	f.archive(t, "s1", f.now.Add(-retention-time.Hour))
	return f
}
func (f *fixture) archive(t *testing.T, id string, at time.Time) {
	t.Helper()
	if err := f.store.ArchiveSession(t.Context(), "fake", id, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SQL().ExecContext(t.Context(), `UPDATE archived_session SET archived_at = ? WHERE platform = 'fake' AND session_id = ?`, at.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) sweep(t *testing.T, want int) {
	t.Helper()
	if err := f.m.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.h.removed) != want {
		t.Fatalf("removed %d, want %d", len(f.h.removed), want)
	}
}

func TestArchiveCleanupDeadlineAndRecovery(t *testing.T) {
	f := newFixture(t)
	f.archive(t, "s1", f.now)
	f.sweep(t, 0)
	f.now = f.now.Add(retention - time.Millisecond)
	f.sweep(t, 0)
	f.now = f.now.Add(time.Millisecond)
	f.sweep(t, 1)
	checkpoint, err := f.m.checkpoint(t.Context(), "local", testPath)
	if err != nil || checkpoint == nil || checkpoint.Branch != "retained" {
		t.Fatalf("missing recovery identity: %+v %v", checkpoint, err)
	}
	archives, err := f.store.ArchivedSessions(t.Context())
	if err != nil || len(archives) != 1 {
		t.Fatalf("cleanup deleted history/archive: %v %v", archives, err)
	}
	f.sweep(t, 1) // idempotent after restart/removal
	_, release, err := f.m.BeginSession(t.Context(), f.p, "s1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(f.h.restored) != 1 {
		t.Fatal("resume did not restore checkout")
	}
	checkpoint, err = f.m.checkpoint(t.Context(), "local", testPath)
	if err != nil || checkpoint != nil {
		t.Fatalf("receipt not cleared after restore: %v %v", checkpoint, err)
	}
	f.sweep(t, 1) // resumed activity starts a fresh grace period
}

func TestActivityAndExternalUpdatesResetDeadlineButPollingDoesNot(t *testing.T) {
	for _, touch := range []string{"session", "directory", "native update", "native message"} {
		t.Run(touch, func(t *testing.T) {
			f := newFixture(t)
			f.archive(t, "s1", f.now)
			f.sweep(t, 0)
			f.now = f.now.Add(6 * 24 * time.Hour)
			switch touch {
			case "session":
				_, release, err := f.m.BeginSession(t.Context(), f.p, "s1")
				if err != nil {
					t.Fatal(err)
				}
				release()
			case "directory":
				_, release, err := f.m.BeginDirectory(t.Context(), testPath)
				if err != nil {
					t.Fatal(err)
				}
				release()
			case "native update":
				f.p.SessionList[0].TimeUpdated = 101
				f.sweep(t, 0)
			case "native message":
				f.p.SessionList[0].LastMessageAt = 101
				f.sweep(t, 0)
			}
			f.now = f.now.Add(2 * 24 * time.Hour)
			f.sweep(t, 0)
			f.now = f.now.Add(5 * 24 * time.Hour)
			f.sweep(t, 1)
		})
	}
}

func TestUnarchiveCancelsAndRearchiveRestartsDeadline(t *testing.T) {
	f := newFixture(t)
	if err := f.store.UnarchiveSession(t.Context(), "fake", "s1"); err != nil {
		t.Fatal(err)
	}
	f.sweep(t, 0)
	f.archive(t, "s1", f.now)
	f.sweep(t, 0)
	f.now = f.now.Add(retention)
	f.sweep(t, 1)
}

func TestCleanupSafetyDeferrals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fixture)
	}{
		{"busy", func(f *fixture) { f.p.SessionList[0].Status = db.StatusBusy }},
		{"waiting", func(f *fixture) { f.p.SessionList[0].Status = db.StatusWaiting }},
		{"unknown status", func(f *fixture) { f.p.SessionList[0].Status = "" }},
		{"permission", func(f *fixture) { f.p.SessionList[0].PendingPermission = true }},
		{"question", func(f *fixture) { f.p.SessionList[0].PendingQuestion = true }},
		{"background task", func(f *fixture) { f.p.SessionList[0].BackgroundTaskCount = 1 }},
		{"service", func(f *fixture) { f.p.SessionList[0].BackgroundServiceCount = 1 }},
		{"stale", func(f *fixture) { f.p.SessionList[0].Stale = true }},
		{"main checkout", func(f *fixture) { f.h.entries[0].Main = true }},
		{"bare", func(f *fixture) { f.h.entries[0].Bare = true }},
		{"locked", func(f *fixture) { f.h.entries[0].Locked = true }},
		{"detached", func(f *fixture) { f.h.entries[0].Branch = "" }},
		{"missing head", func(f *fixture) { f.h.entries[0].Head = "" }},
		{"inventory error", func(f *fixture) { f.p.SessionsErr = errors.New("offline") }},
		{"incomplete import", func(f *fixture) { f.m.d.Ready = func(string) bool { return false } }},
		{"git error", func(f *fixture) { f.h.listErr = errors.New("git unavailable") }},
		{"removal refused", func(f *fixture) { f.h.removeErr = errors.New("dirty") }},
		{"snoozed", func(f *fixture) { _, _ = f.store.SQL().Exec(`UPDATE archived_session SET snoozed_until = 123`) }},
		{"unarchived sibling", func(f *fixture) {
			f.p.SessionList = append(f.p.SessionList, db.Session{ID: "sibling", Directory: testPath, Status: db.StatusDone})
		}},
		{"unarchived nested session", func(f *fixture) {
			f.p.SessionList = append(f.p.SessionList, db.Session{ID: "nested", Directory: testPath + "/nested", Status: db.StatusDone})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { f := newFixture(t); tc.setup(f); f.sweep(t, 0) })
	}
}

func TestSharedAndChildSessions(t *testing.T) {
	f := newFixture(t)
	f.p.SessionList = append(f.p.SessionList, db.Session{ID: "sibling", Directory: testPath, Status: db.StatusDone, TimeUpdated: 100})
	f.archive(t, "sibling", f.now.Add(-retention))
	f.p.SessionList = append(f.p.SessionList, db.Session{ID: "child", ParentID: "s1", SourceChild: true, Directory: testPath, Status: db.StatusDone, TimeUpdated: 100})
	f.sweep(t, 1)
	if len(f.h.removed[0].Sessions) != 3 {
		t.Fatal("missing shared session guards")
	}
}

func TestOwnerSnapshotRecheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fixture)
	}{
		{"safe", nil},
		{"new session", func(f *fixture) {
			f.p.SessionList = append(f.p.SessionList, db.Session{ID: "new", Directory: testPath, Status: db.StatusDone})
		}},
		{"changed activity", func(f *fixture) { f.p.SessionList[0].TimeUpdated++ }},
		{"lifecycle failure", func(f *fixture) { f.p.lifecycleErr = errors.New("unavailable") }},
		{"lifecycle busy", func(f *fixture) { f.p.lifecycle = &platforms.SessionLifecycle{Status: db.StatusBusy} }},
		{"external process", func(f *fixture) { f.p.lifecycle = &platforms.SessionLifecycle{Status: db.StatusDone, ReadOnly: true} }},
		{"live task", func(f *fixture) { f.p.Live = &platforms.LiveState{BackgroundTaskCount: 1} }},
		{"native queue", func(f *fixture) { f.p.native = []platforms.NativeQueuedMessage{{ID: "held"}} }},
		{"native read failure", func(f *fixture) { f.p.nativeErr = errors.New("offline") }},
		{"incomplete", func(f *fixture) { f.m.d.Ready = func(string) bool { return false } }},
		{"disappeared", func(f *fixture) { f.p.SessionList = nil }},
		{"recent touch", func(f *fixture) { _ = f.m.touch(t.Context(), state.Key{Platform: "fake", SessionID: "s1"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			req := hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Project: "/projects/repo", Path: testPath, Branch: "retained", Head: "abc"}, Sessions: []hostsvc.WorktreeSessionCheck{{Platform: "fake", SessionID: "s1", TimeUpdated: 100}}}
			if tc.setup != nil {
				tc.setup(f)
			}
			_, release, err := f.m.AdmitCleanup(t.Context(), req)
			if err == nil {
				release()
			}
			if (err == nil) != (tc.name == "safe") {
				t.Fatalf("AdmitCleanup = %v", err)
			}
		})
	}
}

func TestFailedCleanupRecordsReasonAndRecoversPendingReceipt(t *testing.T) {
	f := newFixture(t)
	f.h.removeErr = fmt.Errorf("ignored secret")
	f.sweep(t, 0)
	var reason string
	if err := f.store.SQL().QueryRow(`SELECT reason FROM worktree_cleanup_checkout`).Scan(&reason); err != nil || reason != "ignored secret" {
		t.Fatalf("reason %q: %v", reason, err)
	}
	f.h.restoreErr = errors.New("owner offline")
	_, _, err := f.m.BeginSession(t.Context(), f.p, "s1")
	if err == nil {
		t.Fatal("resume ignored recovery failure")
	}
	f.h.restoreErr = nil
	_, release, err := f.m.BeginDirectory(t.Context(), testPath)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestRunCancellationAndDisabledStore(t *testing.T) {
	m := New(Deps{})
	m.Run(t.Context())
	if err := m.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, release, err := m.BeginSession(t.Context(), nil, "s")
	if err != nil {
		t.Fatal(err)
	}
	release()
	_, release, err = m.BeginDirectory(t.Context(), testPath)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, _, err := m.AdmitCleanup(t.Context(), hostsvc.CleanupWorktreeRequest{}); err == nil {
		t.Fatal("cleanup without durable state")
	}
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.m.Run(ctx)
	if _, _, err := f.m.AdmitCleanup(t.Context(), hostsvc.CleanupWorktreeRequest{}); err == nil {
		t.Fatal("cleanup without safety snapshot")
	}
}
