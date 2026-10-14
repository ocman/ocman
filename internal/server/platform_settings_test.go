package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type authoritativeReloadPlatform struct{ *fakePlatform }

func (authoritativeReloadPlatform) ReloadIdle(context.Context) (bool, error) {
	return false, errors.New("project status unavailable")
}

func TestConfigReloadUsesAuthoritativeStatusDespiteSuccessfulBrowsing(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	srv.registry = platforms.NewRegistry()
	srv.registry.Register(authoritativeReloadPlatform{&fakePlatform{id: "claudecode", sessions: []db.Session{{Status: db.StatusDone}}}})
	if idle, err := srv.configIdle(t.Context()); idle || err == nil {
		t.Fatalf("browsing hid project-status error: idle=%v err=%v", idle, err)
	}
}

func TestConfigReloadAllowsFinishedWaitingConversationsWithoutPrompts(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	srv.registry = platforms.NewRegistry()
	status := db.SettleSessionStatus(db.TurnSettled, true, db.InferSessionStatus("assistant", "stop", "", false))
	p := &fakePlatform{id: "opencode", sessions: []db.Session{{Status: status}}}
	srv.registry.Register(p)
	if idle, err := srv.configIdle(t.Context()); !idle || err != nil {
		t.Fatalf("finished assistant turn blocks reload: %v %v", idle, err)
	}
	for _, pending := range []db.Session{{Status: status, PendingPermission: true}, {Status: status, PendingQuestion: true}} {
		p.sessions = []db.Session{pending}
		if idle, err := srv.configIdle(t.Context()); idle || err != nil {
			t.Fatalf("pending prompt ignored: %v %v", idle, err)
		}
	}
}

func TestPlatformSettingsOwnerLocalRoute(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		peer, origin string
		want         int
	}{
		{"127.0.0.1:1234", "", 200},
		{"192.0.2.1:1234", "", 403},
		{"127.0.0.1:1234", "https://example.com", 403},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/settings/platforms", strings.NewReader(`{"enabled":["claudecode"]}`))
		r.RemoteAddr = tc.peer
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("peer %s origin %s: %d %s", tc.peer, tc.origin, w.Code, w.Body.String())
		}
	}
}

func TestConfigReloadWaitsForLocalTurnsPromptsAndQueue(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	srv.registry = platforms.NewRegistry()
	local := &fakePlatform{id: "opencode"}
	srv.registry.Register(local)
	srv.registry.Register(&fakePlatform{id: "r-other:opencode", sessions: []db.Session{{Status: db.StatusBusy}}})
	for _, session := range []db.Session{
		{Status: db.StatusBusy}, {Status: db.StatusLimited},
		{Status: db.StatusDone, PendingPermission: true}, {Status: db.StatusDone, PendingQuestion: true},
	} {
		local.sessions = []db.Session{session}
		if idle, err := srv.configIdle(t.Context()); err != nil || idle {
			t.Fatalf("active session: %v %v", idle, err)
		}
	}
	local.sessions = []db.Session{{Status: db.StatusDone}}
	local.sessionsErr = errors.New("read failed")
	if idle, err := srv.configIdle(t.Context()); idle || err == nil {
		t.Fatalf("uncertain: %v %v", idle, err)
	}
	local.sessionsErr = nil
	if idle, err := srv.configIdle(t.Context()); !idle || err != nil {
		t.Fatalf("settled: %v %v", idle, err)
	}
	if err := srv.stateDB.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "q", Platform: "opencode", SessionID: "s", Text: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	if idle, err := srv.configIdle(t.Context()); idle || err != nil {
		t.Fatalf("queued: %v %v", idle, err)
	}
	if _, err := srv.stateDB.DeleteQueuedMessage(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "q-remote", Platform: "r-other:opencode", SessionID: "s", Text: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	if idle, err := srv.configIdle(t.Context()); !idle || err != nil {
		t.Fatalf("remote queue: %v %v", idle, err)
	}
	finish, err := srv.beginConfigTurn(t.Context(), "r-other:opencode")
	if err != nil {
		t.Fatal(err)
	}
	finish()
	_ = srv.stateDB.Close()
	if idle, err := srv.configIdle(t.Context()); idle || err == nil {
		t.Fatalf("closed store: %v %v", idle, err)
	}
}

func TestRebasedQueueDrainsDuringPlatformReload(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	srv.registry = platforms.NewRegistry()
	sent := 0
	p := &fakePlatform{id: "opencode",
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo", Status: db.StatusDone}}, nil
		},
		sendMessageFn: func(platforms.SendMessageRequest) error { sent++; return nil },
	}
	srv.registry.Register(p)
	srv.WithConfigReload(func() {}, "opencode,claudecode", false)
	req := platforms.SendMessageRequest{SessionID: "s", Message: "existing", Model: "provider/model"}
	if err := srv.queueSvc().Enqueue(t.Context(), "opencode", true, req); err != nil {
		t.Fatal(err)
	}
	direct, err := srv.prepareSendNow(t.Context(), "opencode", req)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/settings/platforms", strings.NewReader(`{"enabled":["claudecode"]}`))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if err := direct(t.Context()); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("prepared direct send admitted: %v", err)
	}
	if err := srv.queueSvc().Enqueue(t.Context(), "opencode", true, req); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("new queued send admitted: %v", err)
	}
	srv.queueSvc().Flush(t.Context(), "opencode", "s")
	if sent != 1 {
		t.Fatalf("existing queue sent %d times, want 1", sent)
	}
	if _, err := srv.prepareQueuedSend(t.Context(), "missing", req); err == nil {
		t.Fatal("unknown platform prepared")
	}
}
