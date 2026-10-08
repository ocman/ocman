package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type injectedDeadlineContext struct{ context.Context }

func (c injectedDeadlineContext) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestConfirmationDrainsAllPagesWithoutCountCutoff(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	sessions := make([]db.Session, 1025)
	for i := range sessions {
		sessions[i] = db.Session{ID: fmt.Sprintf("s%04d", i), Directory: "/repo"}
	}
	reads := 0
	confirming := false
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: sessions}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
		if confirming {
			reads++
		}
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m-" + id}, nil
	}})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	confirming = true
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	if reads != len(sessions) {
		t.Fatalf("confirmation missed or reread candidates: %d", reads)
	}
	if got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", sessions[1024].ID); err != nil || len(got) != 1 {
		t.Fatalf("last batch lost: %v %v", got, err)
	}
}

func TestConfirmationNewMembershipSurvivesFailedReadAndReopen(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	path := filepath.Join(t.TempDir(), "membership-progress.db")
	durable, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv.stateDB = durable
	defer func() { _ = srv.stateDB.Close() }()
	lookups := make(map[string]int)
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(_ context.Context, dir string) (string, error) { lookups[dir]++; return "/repo", nil }})
	sessions := []db.Session{{ID: "a", Directory: "/repo"}}
	var cancelRead context.CancelFunc
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessionsHook: func(context.Context, string, int64) ([]db.Session, error) { return sessions, nil }}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
		if id == "b" && cancelRead != nil {
			cancelRead()
			return nil, context.DeadlineExceeded
		}
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m-" + id}, nil
	}})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	sessions = append(sessions, db.Session{ID: "b", Directory: "/new-dir"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cancelRead = cancel
	if err := srv.confirmOpencodeReplacement(ctx, "/repo"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost failed lifecycle read: %v", err)
	}
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	srv.stateDB, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := srv.stateDB.ReplacementReconciliationBatch(t.Context(), "opencode", "/repo", 64)
	if err != nil || len(batch) != 1 || batch[0].Member == nil || !*batch[0].Member {
		t.Fatalf("completed membership was lost: %v %v", batch, err)
	}
	cancelRead = nil
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	if lookups["/new-dir"] != 1 {
		t.Fatalf("retry repeated completed project resolution: %v", lookups)
	}
	if got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "b"); err != nil || len(got) != 1 {
		t.Fatalf("new member history lost: %v %v", got, err)
	}
}

func TestConfirmationCheckpointResumesAfterReopen(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation", true: "deadline"}[deadline], func(t *testing.T) {
			srv, reg := newInterruptionTestServer(t)
			path := filepath.Join(t.TempDir(), "progress-state.db")
			durable, err := state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			srv.stateDB = durable
			defer func() { _ = srv.stateDB.Close() }()
			resolutions, lists := 0, 0
			srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(context.Context, string) (string, error) { resolutions++; return "/repo", nil }})
			calls := make(map[string]int)
			var cancelRead context.CancelFunc
			sessions := []db.Session{{ID: "a", Directory: "/repo"}, {ID: "b", Directory: "/repo"}, {ID: "c", Directory: "/repo"}}
			reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessionsHook: func(context.Context, string, int64) ([]db.Session, error) { lists++; return sessions, nil }}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
				if cancelRead != nil {
					calls[id]++
					if id == "b" {
						cancelRead()
					}
				}
				return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m-" + id}, nil
			}})
			if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "original restart"); err != nil {
				t.Fatal(err)
			}
			if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			stopAt, original, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
			if err != nil {
				t.Fatal(err)
			}
			beforeResolutions := resolutions
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelRead = cancel
			first := ctx
			wantErr := context.Canceled
			if deadline {
				first = injectedDeadlineContext{ctx}
				wantErr = context.DeadlineExceeded
			}
			if err := srv.confirmOpencodeReplacement(first, "/repo"); !errors.Is(err, wantErr) {
				t.Fatalf("lost interruption signal: %v", err)
			}
			for _, id := range []string{"a", "b", "c"} {
				if notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id); err != nil || len(notices) != 0 {
					t.Fatalf("partial history published: %v %v", notices, err)
				}
			}
			persistedAt, persisted, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
			if err != nil || persistedAt != stopAt || persisted["a"] != original["a"] {
				t.Fatalf("checkpoint changed original evidence: %d %v %v", persistedAt, persisted, err)
			}
			firstLists := lists
			if err := srv.stateDB.Close(); err != nil {
				t.Fatal(err)
			}
			srv.stateDB, err = state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			cancelRead = func() {}
			if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"a", "b", "c"} {
				if calls[id] != 1 {
					t.Errorf("%s reconciliation restarted: %d reads", id, calls[id])
				}
				if notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id); err != nil || len(notices) != 1 {
					t.Fatalf("missing reconciled history: %v %v", notices, err)
				}
			}
			if resolutions != beforeResolutions || lists != firstLists {
				t.Errorf("retry repeated historical work: resolutions=%d want=%d lists=%d want=%d", resolutions, beforeResolutions, lists, firstLists)
			}
			if stopAt == 0 || original["a"].MessageID != "m-a" {
				t.Fatal("missing original stop evidence")
			}
		})
	}
}
