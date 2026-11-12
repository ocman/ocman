package server

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type watchConnectionNative struct{ *historyTestPlatform }

func (*watchConnectionNative) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &platforms.SessionLifecycle{Status: db.StatusWaiting}, nil
}

func TestPRWatchDeliversSavedKindNoticeToRecordedConnection(t *testing.T) {
	s, native, id := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	p := &watchConnectionNative{&historyTestPlatform{fakePlatform: native}}
	adapter, err := owned.New(t.Context(), p, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Unregister("test")
	if err := s.registry.RegisterConnection("cnx_work", adapter); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	s.registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "owner" && kind == "test" && source == "source" {
			return "cnx_work", nil
		}
		return "", platforms.ErrNotFound
	})
	var sent []string
	native.SendMessageFn = func(req platforms.SendMessageRequest) error {
		if req.SessionID != "native" {
			t.Errorf("native send received %q", req.SessionID)
		}
		sent = append(sent, req.Message)
		return nil
	}
	const notice = "PR #42 update: All reported checks passed."
	l := prworkflow.Link{Platform: "test", SessionID: id, RemoteID: "local", Number: 42, Remote: forge.Remote{Host: "forge.example", Repo: "team/repo"}}
	if route, err := s.prWatchRoute(t.Context(), "test", id); err != nil || route != "cnx_work" {
		t.Fatalf("route=%q err=%v", route, err)
	}
	payload, err := json.Marshal(struct {
		prworkflow.Link
		Pending string
	}{l, notice})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.stateDB.SQL().ExecContext(t.Context(), `INSERT INTO session_pull_request VALUES(?,?,?,?,?,?)`, "test", id, "forge.example", "team/repo", 42, string(payload)); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); s.prWorkflowModule().Run(ctx) }()
		defer func() { cancel(); <-done }()
		synctest.Wait()
		if len(sent) != 1 || sent[0] != notice {
			t.Fatalf("sent=%v; completion notice must reach the recorded account", sent)
		}
		var pending string
		if err := s.stateDB.SQL().QueryRowContext(ctx, `SELECT json_extract(payload, '$.Pending') FROM session_pull_request WHERE session_id=?`, id).Scan(&pending); err != nil || pending != "" {
			t.Fatalf("pending=%q err=%v", pending, err)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if len(sent) != 1 {
			t.Fatalf("duplicate completion notice: %v", sent)
		}
	})
	if route, err := s.prWatchRoute(t.Context(), "cnx_work", id); err != nil || route != "cnx_work" {
		t.Fatalf("named scope: route=%q err=%v", route, err)
	}
	for _, scope := range []string{"cnx_other", "other-kind", "r-disconnected:test"} {
		if _, err := s.prWatchRoute(t.Context(), scope, id); err == nil {
			t.Fatalf("explicit scope %q fell back to the recorded connection", scope)
		}
	}
	s.registry.Unregister("cnx_work")
	if s.prWatchReady(t.Context(), prworkflow.Target{Platform: "test", SessionID: id, RemoteID: "local"}) {
		t.Fatal("missing recorded account admitted")
	}
}
