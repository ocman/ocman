package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

type prLifecyclePlatform struct {
	*fakePlatform
	life *platforms.SessionLifecycle
	err  error
}

func (p prLifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return p.life, p.err
}

func TestPRTargetAndOwnership(t *testing.T) {
	s := testServer(t)
	p := &fakePlatform{PlatformID: "fake", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo"}}, nil
	}}
	s.registry.Register(p)
	req := httptest.NewRequest(http.MethodGet, "/api/session/s1/pull-requests?platform=fake", nil)
	target, ok := s.prTarget(httptest.NewRecorder(), req, "s1")
	if !ok || target.RemoteID != "local" || target.Directory != "/repo" {
		t.Fatal(target, ok)
	}
	if _, err := s.prOwner(t.Context(), prworkflow.Target{Platform: "fake", RemoteID: "other"}); err == nil {
		t.Fatal("mismatch accepted")
	}
	if _, err := s.prOwner(t.Context(), prworkflow.Target{Platform: "r-box:fake", RemoteID: "other"}); err == nil {
		t.Fatal("mismatch accepted")
	}
	if _, err := s.prOwner(t.Context(), prworkflow.Target{Platform: "r-box:fake", RemoteID: "box"}); err == nil {
		t.Fatal("disconnected owner accepted")
	}
	remoteReq := httptest.NewRequest(http.MethodGet, "/api/session/s1/pull-requests?platform=r-box:fake", nil)
	w := httptest.NewRecorder()
	if _, ok := s.prTarget(w, remoteReq, "s1"); ok || w.Code != 503 {
		t.Fatal(w.Code)
	}
	p.SessionFn = func(string) (*platforms.SessionDetail, error) { return nil, errors.New("missing") }
	if _, ok := s.prTarget(httptest.NewRecorder(), req, "s1"); ok {
		t.Fatal("missing session accepted")
	}
	if _, ok := s.prTarget(httptest.NewRecorder(), req, "bad/id"); ok {
		t.Fatal("invalid ID accepted")
	}
	if s.prWorkflowModule().Name() != "pr-workflow" {
		t.Fatal("missing module")
	}
}

func TestPRWatchAdmission(t *testing.T) {
	s := testServer(t)
	target := prworkflow.Target{Platform: "fake", SessionID: "s1", RemoteID: "local"}
	if s.prWatchReady(t.Context(), target) {
		t.Fatal("missing platform admitted")
	}
	p := &fakePlatform{PlatformID: "fake"}
	s.registry.Register(p)
	if s.prWatchReady(t.Context(), target) {
		t.Fatal("missing lifecycle admitted")
	}
	life := &platforms.SessionLifecycle{Status: db.SessionStatus("done")}
	adapter := prLifecyclePlatform{fakePlatform: p, life: life}
	s.registry.Register(adapter)
	if !s.prWatchReady(t.Context(), target) {
		t.Fatal("idle session not admitted")
	}
	for _, status := range []db.SessionStatus{"busy", "waiting"} {
		life.Status = status
		if s.prWatchReady(t.Context(), target) {
			t.Fatal(status)
		}
	}
	life.Status = "done"
	life.ReadOnly = true
	if s.prWatchReady(t.Context(), target) {
		t.Fatal("read-only admitted")
	}
	life.ReadOnly = false
	if err := s.stateDB.ArchiveSession(t.Context(), "fake", "s1", 0); err != nil {
		t.Fatal(err)
	}
	if s.prWatchReady(t.Context(), target) {
		t.Fatal("archived session admitted")
	}
}
