package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

type watchUpdateForge struct{ forge.Forge }

func (watchUpdateForge) LookupPR(context.Context, string, int) (forge.PR, error) {
	mergeable := true
	return forge.PR{Number: 42, Status: "open", HeadSHA: "abc", Mergeable: &mergeable}, nil
}

func (watchUpdateForge) Checks(context.Context, string, string) (forge.CIStatus, forge.RateLimit, error) {
	return forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "ocman/review", State: forge.CIStateFailure}}}, forge.RateLimit{}, nil
}

// Exercise a detected check change through real readiness and the held queue.
// Finishing an ordinary assistant turn must release the durable watch notice,
// even when no more foreground idle events arrive after the watch polls.
func TestPRWatchResumesAfterNormalAssistantCompletion(t *testing.T) {
	s, q := newQueueFake(t, false)
	// Keep the queue fixture's send implementation while adding lifecycle reads.
	p, _ := s.registry.Get("fake")
	life := prLifecyclePlatform{fakePlatform: p.(*fakePlatform), life: &platforms.SessionLifecycle{Status: db.StatusBusy}}
	s.registry.Register(life)
	l := prworkflow.Link{
		Platform: "fake", SessionID: "s1", RemoteID: "local", Number: 42, Watching: true,
		Remote: forge.Remote{Host: "forge.example", Repo: "team/repo"},
	}
	payload, err := json.Marshal(struct {
		prworkflow.Link
		Snapshot *prworkflow.Snapshot
	}{l, &prworkflow.Snapshot{SHA: "abc", Checks: forge.CIStatePending}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stateDB.SQL().ExecContext(t.Context(), `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?)`, l.Platform, l.SessionID, l.Remote.Host, l.Remote.Repo, l.Number, string(payload)); err != nil {
		t.Fatal(err)
	}
	assertState := func(t *testing.T, wantPending bool, wantQueued int) {
		t.Helper()
		var pending string
		if err := s.stateDB.SQL().QueryRowContext(t.Context(), `SELECT json_extract(payload, '$.Pending') FROM session_pull_request WHERE session_id='s1'`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		queued, err := s.stateDB.CountQueuedMessages(t.Context(), "fake", "s1")
		if err != nil || (pending != "") != wantPending || queued != wantQueued {
			t.Fatalf("pending=%q queued=%d err=%v; want pending=%v queued=%d", pending, queued, err, wantPending, wantQueued)
		}
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m := prworkflow.New(prworkflow.Deps{
			DB: s.stateDB, Owner: s.prOwner, Ready: s.prWatchReady,
			ResolveForge: func(forge.Remote) (forge.Forge, bool) { return watchUpdateForge{}, true },
			Enqueue: func(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
				return s.queueSvc().Enqueue(ctx, platform, false, req)
			},
		})
		done := make(chan struct{})
		go func() { defer close(done); m.Run(ctx) }()
		defer func() { cancel(); <-done }()
		synctest.Wait()
		assertState(t, true, 0)
		life.life = &platforms.SessionLifecycle{Status: db.SettleSessionStatus(db.TurnSettled, true, db.InferSessionStatus("assistant", "stop", "", false))}
		s.registry.Register(life)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		assertState(t, false, 0)
		if sent := q.sentMessages(); len(sent) != 1 || !strings.Contains(sent[0], "ocman/review") {
			t.Fatalf("sent=%v", sent)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		assertState(t, false, 0)
		if len(q.sentMessages()) != 1 {
			t.Fatal("duplicate watch delivery")
		}
	})
}
