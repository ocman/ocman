package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// refresh=1 must use the authoritative list, never the observed cache:
// the frontend dismisses a prompt when it is absent from this response.
func TestHandleSessionPermissionsRefresh(t *testing.T) {
	const sid = "sess-1"
	sessions := []db.Session{mkSession("fake", sid, "t", 1000)}
	cases := []struct {
		name     string
		adapter  platforms.Platform
		query    string
		wantCode int
		wantBody string
	}{
		{"cached list by default", &refreshingInboxPlatform{fakePlatform: fakePlatform{id: "fake", sessions: sessions}, prompts: []platforms.LivePrompt{{"id": "live"}}}, "", http.StatusOK, "[]"},
		{"live list on refresh", &refreshingInboxPlatform{fakePlatform: fakePlatform{id: "fake", sessions: sessions}, prompts: []platforms.LivePrompt{{"id": "live"}}}, "?refresh=1", http.StatusOK, `"live"`},
		{"refresh failure is an error", &refreshingInboxPlatform{fakePlatform: fakePlatform{id: "fake", sessions: sessions}, err: platforms.ErrPlatformUnreachable}, "?refresh=1", http.StatusServiceUnavailable, ""},
		{"no authoritative list", &fakePlatform{id: "fake", sessions: sessions}, "?refresh=1", http.StatusNotImplemented, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			reg.Register(c.adapter)
			rr := httptest.NewRecorder()
			srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodGet, "/api/session/"+sid+"/permissions"+c.query, nil))
			if rr.Code != c.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, c.wantCode, rr.Body)
			}
			if c.wantBody != "" && !strings.Contains(rr.Body.String(), c.wantBody) {
				t.Fatalf("body = %s, want %s", rr.Body, c.wantBody)
			}
		})
	}
}
