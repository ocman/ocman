package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func newForkMoveTestServer(t *testing.T, fake *fakePlatform) *Server {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	fake.sessions = []db.Session{mkSession(string(fake.ID()), "sess-1", "t", 1000)}
	reg.Register(fake)
	return srv
}

func TestSessionFork_Post(t *testing.T) {
	var got *platforms.ForkSessionRequest
	fake := &fakePlatform{
		id: "fake",
		forkSessionFn: func(req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
			got = &req
			return &platforms.CreateSessionResponse{ID: "sess-forked"}, nil
		},
	}
	srv := newForkMoveTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/session/sess-1/fork", strings.NewReader(`{"messageID":"msg-9"}`))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.ID != "sess-forked" {
		t.Errorf("resp.id = %q, want sess-forked", resp.ID)
	}
	if got == nil || got.SessionID != "sess-1" || got.MessageID != "msg-9" {
		t.Fatalf("adapter got %+v, want sess-1/msg-9", got)
	}
}

func TestSessionMove_Post(t *testing.T) {
	var got *platforms.MoveSessionRequest
	fake := &fakePlatform{
		id: "fake",
		moveSessionFn: func(req platforms.MoveSessionRequest) error {
			got = &req
			return nil
		},
	}
	srv := newForkMoveTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/session/sess-1/move", strings.NewReader(`{"directory":"/tmp/dst"}`))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if got == nil || got.SessionID != "sess-1" || got.Directory != "/tmp/dst" {
		t.Fatalf("adapter got %+v, want sess-1 -> /tmp/dst", got)
	}
}

func TestSessionMove_MissingDirectoryIsValidationError(t *testing.T) {
	called := false
	fake := &fakePlatform{
		id: "fake",
		moveSessionFn: func(platforms.MoveSessionRequest) error {
			called = true
			return nil
		},
	}
	srv := newForkMoveTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/session/sess-1/move", strings.NewReader(`{"directory":"  "}`))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body)
	}
	if called {
		t.Error("adapter MoveSession should not be reached on validation failure")
	}
}
