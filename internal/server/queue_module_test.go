package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

func TestQueueModuleRoutesThroughServer(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/session/s1/queue?platform=r-gone:opencode", "", 200},
		{http.MethodDelete, "/api/session/s1/queue/unknown?platform=r-gone:opencode", "", 204},
		{http.MethodPost, "/api/session/s1/queue/unknown/move", `{"direction":1}`, 204},
		{http.MethodGet, "/api/session/bad%20id/queue", "", 400},
		{http.MethodGet, "/api/settings/model-fallthrough", "", 200},
		{http.MethodPost, "/api/session/s1/queue", "", 405},
		{http.MethodGet, "/api/session/s1/queue/q1", "", 405},
		{http.MethodDelete, "/api/session/s1/queue/q1/move", "", 405},
	} {
		rec := httptest.NewRecorder()
		serveRoutes(t, s, rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if rec.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	s.auth = newTestAuth(t, "secret")
	for _, path := range []string{"/api/session/s1/queue", "/api/settings/model-fallthrough"} {
		rec := httptest.NewRecorder()
		serveRoutes(t, s, rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unguarded %s: %d", path, rec.Code)
		}
	}
}

func TestQueueCooldownVisibleBeforeOrderedBusSubscribers(t *testing.T) {
	r := newCooldownRig(t)
	r.last = quota429("a")
	var order []int
	for _, n := range []int{1, 2} {
		r.srv.bus().OnSessionIdle(func(ctx context.Context, e module.SessionIdle) {
			if e.Platform != "opencode" || e.SessionID != "s1" {
				t.Errorf("owner lost: %+v", e)
			}
			model, _, ok := r.srv.sessions.Fallthrough(ctx, e.Platform, e.SessionID)
			if !ok || model != "b/y" {
				t.Errorf("cooldown not visible: model=%q ok=%v", model, ok)
			}
			order = append(order, n)
		})
	}
	r.srv.onSessionIdle("opencode", "s1")
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatal(order)
	}
	if err := r.srv.queueModule().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Idle-edge subscribers receive the server's lifetime, so work they derive
// from it stops at shutdown.
func TestSessionIdlePublishesServerLifetime(t *testing.T) {
	r := newCooldownRig(t)
	lifetime, cancel := context.WithCancel(t.Context())
	cancel()
	r.srv.lifetimeMu.Lock()
	r.srv.lifetimeCtx = lifetime
	r.srv.lifetimeMu.Unlock()
	var got error
	r.srv.bus().OnSessionIdle(func(ctx context.Context, _ module.SessionIdle) { got = ctx.Err() })
	r.srv.onSessionIdle("opencode", "s1")
	if got == nil {
		t.Fatal("SessionIdle was published without the server lifetime")
	}
}
