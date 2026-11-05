package worktreecleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestExistingArchivesReceiveUpgradeGracePeriod(t *testing.T) {
	path := statetest.Path(t)
	store, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveSession(t.Context(), "fake", "s1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL().Exec(`UPDATE archived_session SET archived_at = 1`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var at int64
	if err := store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if time.Since(time.UnixMilli(at)) > time.Minute {
		t.Fatalf("upgrade did not grant grace: %d", at)
	}
}

func TestSeenPinAndQueueActionsCountButReadsDoNot(t *testing.T) {
	for _, action := range []string{"seen", "pin", "unpin", "enqueue", "delete queue", "reorder queue"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			if action == "unpin" {
				if err := f.store.PinSession(ctx, "fake", "s1"); err != nil {
					t.Fatal(err)
				}
			}
			if action == "delete queue" || action == "reorder queue" {
				for _, id := range []string{"q1", "q2"} {
					if err := f.store.EnqueueMessage(ctx, state.QueuedMessage{ID: id, Platform: "fake", SessionID: "s1", Text: "held", ImagesJSON: "[]", CreatedAt: time.Now().UnixMilli()}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := f.store.SQL().Exec(`DELETE FROM worktree_cleanup_activity`); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "seen":
				err := f.store.MarkSessionSeen(ctx, "fake", "s1", 100)
				if err != nil {
					t.Fatal(err)
				}
			case "pin":
				err := f.store.PinSession(ctx, "fake", "s1")
				if err != nil {
					t.Fatal(err)
				}
			case "unpin":
				err := f.store.UnpinSession(ctx, "fake", "s1")
				if err != nil {
					t.Fatal(err)
				}
			case "enqueue":
				err := f.store.EnqueueMessage(ctx, state.QueuedMessage{ID: "q", Platform: "fake", SessionID: "s1", Text: "held", ImagesJSON: "[]", CreatedAt: time.Now().UnixMilli()})
				if err != nil {
					t.Fatal(err)
				}
			case "delete queue":
				if _, err := f.store.DeleteQueuedMessage(ctx, "q1"); err != nil {
					t.Fatal(err)
				}
			case "reorder queue":
				if _, err := f.store.MoveQueuedMessage(ctx, "q2", -1); err != nil {
					t.Fatal(err)
				}
			}
			var before, after int64
			if err := f.store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id='s1'`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if time.Since(time.UnixMilli(before)) > time.Second {
				t.Fatalf("activity missing: %d", before)
			}
			if _, err := f.store.ArchivedSessions(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.SeenSessionStates(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.PinnedSessions(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SQL().QueryRow(`SELECT activity_at FROM worktree_cleanup_activity WHERE session_id='s1'`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("polling reset activity")
			}
			f.sweep(t, 0)
		})
	}
}

func TestRemoteOwnerRoutingAndDisconnection(t *testing.T) {
	f := newFixture(t)
	if err := f.store.UnarchiveSession(t.Context(), "fake", "s1"); err != nil {
		t.Fatal(err)
	}
	f.p.PlatformID = "r-box:fake"
	if err := f.store.ArchiveSession(t.Context(), "r-box:fake", "s1", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SQL().Exec(`UPDATE archived_session SET archived_at = 1`); err != nil {
		t.Fatal(err)
	}
	local := &cleanupHost{}
	router := hostsvc.NewRouter(local)
	f.m.d.Router = func() *hostsvc.Router { return router }
	f.sweep(t, 0)
	if len(local.removed) != 0 {
		t.Fatal("disconnected owner fell back to local")
	}
	router.RegisterRemote("box", f.h)
	f.sweep(t, 1)
	if f.h.removed[0].Sessions[0].Platform != "fake" {
		t.Fatal("compound platform leaked into owner-local check")
	}
	router.UnregisterRemote("box")
	if _, _, err := f.m.BeginSession(t.Context(), f.p, "s1"); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("restore fell back to local: %v", err)
	}
	router.RegisterRemote("box", f.h)
	_, release, err := f.m.BeginOwnerDirectory(t.Context(), "box", testPath)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(f.h.restored) != 1 || len(local.restored) != 0 {
		t.Fatal("restore ran on wrong owner")
	}
}

func TestOwnerCleanupRechecksInventoryAfterCandidateSelection(t *testing.T) {
	f := newFixture(t)
	f.h.beforeRemove = func(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
		f.p.SessionList = append(f.p.SessionList, db.Session{ID: "new", Directory: testPath, Status: db.StatusDone})
		_, release, err := f.m.AdmitCleanup(ctx, req)
		if err == nil {
			release()
		}
		return err
	}
	f.sweep(t, 0)
}

func TestOwnerTaskAndNativeLifecycleGuards(t *testing.T) {
	f := newFixture(t)
	f.m.d.Idle = func(context.Context) error { return errors.New("pending application task") }
	req := hostsvc.CleanupWorktreeRequest{Sessions: []hostsvc.WorktreeSessionCheck{{Platform: "fake", SessionID: "s1", TimeUpdated: 100}}, WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Path: testPath}}
	if _, _, err := f.m.AdmitCleanup(t.Context(), req); err == nil {
		t.Fatal("ignored pending tasks")
	}
	f.m.d.Idle = nil
	f.p.SessionLifecycleFn = func(string) (db.SessionLifecycle, error) {
		return db.SessionLifecycle{Directory: testPath, Status: db.StatusBusy}, nil
	}
	if _, _, err := f.m.AdmitCleanup(t.Context(), req); err == nil {
		t.Fatal("ignored fresh native execution")
	}
	f.p.SessionLifecycleFn = func(string) (db.SessionLifecycle, error) {
		return db.SessionLifecycle{Directory: testPath, Status: db.StatusDone, LatestMessageCreated: 101}, nil
	}
	if _, _, err := f.m.AdmitCleanup(t.Context(), req); err == nil {
		t.Fatal("ignored a just-completed native turn not ingested yet")
	}
}

func TestUnsupportedOwnerDoesNotLeaveAnUnusableRecoveryReceipt(t *testing.T) {
	f := newFixture(t)
	f.h.removeErr = platforms.ErrUnsupported
	f.sweep(t, 0)
	checkpoint, err := f.m.checkpoint(t.Context(), "local", testPath)
	if err != nil || checkpoint != nil {
		t.Fatalf("unsupported cleanup left a recovery obligation: %+v %v", checkpoint, err)
	}
	f.h.restoreErr = errors.New("older owner has no restore support")
	_, release, err := f.m.BeginSession(t.Context(), f.p, "s1")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestSessionInTrackedSubdirectoryCleansAndRestoresItsCheckout(t *testing.T) {
	f := newFixture(t)
	f.p.SessionList[0].Directory = testPath + "/tracked/subdir"
	f.sweep(t, 1)
	if f.h.removed[0].Path != testPath {
		t.Fatal("cleanup did not resolve checkout root")
	}
	_, release, err := f.m.BeginSession(t.Context(), f.p, "s1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(f.h.restored) != 1 || f.h.restored[0].Path != testPath {
		t.Fatal("resume did not restore ancestor checkout")
	}
	if receipt, err := f.m.checkpoint(t.Context(), "local", testPath); err != nil || receipt != nil {
		t.Fatalf("ancestor receipt not cleared: %v %v", receipt, err)
	}
	for _, dir := range []string{"/projects/repo", "/projects/.worktrees/repo", "/projects/.worktrees/repo/../escape"} {
		if managedCheckout(dir) != "" {
			t.Fatalf("accepted non-checkout path %q", dir)
		}
	}
}
