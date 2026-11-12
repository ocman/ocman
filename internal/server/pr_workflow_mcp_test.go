package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestPRWorkflowMCPHelpAndDisconnectedOwners(t *testing.T) {
	srv := testServer(t)
	mux := mustRoutes(t, srv)
	for _, tc := range []struct {
		name, args, want string
		fail             bool
	}{
		{"help", `{"action":"help"}`, "checks_prompt", false},
		{"project owner", `{"action":"list","directory":"/repo","remote_id":"offline","remote":"origin"}`, "remote_not_connected", true},
		{"session owner", `{"action":"linked","session_id":"s1","platform":"r-offline:opencode"}`, "Session owner disconnected", true},
		{"lookup owner", `{"action":"linked","directory":"/repo","remote_id":"offline"}`, "session lookup failed", true},
		{"no arbitrary routes", `{"action":"/api/settings/source-control"}`, "unknown action", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pull_requests","arguments":` + tc.args + `}}`
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			text := w.Body.String()
			if w.Code != http.StatusOK || !strings.Contains(text, tc.want) || strings.Contains(text, `"isError":true`) != tc.fail {
				t.Fatalf("%d %s", w.Code, text)
			}
		})
	}
}

func TestPRWorkflowMCPLooksUpSessionWithoutExtraCall(t *testing.T) {
	s := testServer(t)
	resolved := false
	s.registry.Register(&fakePlatform{PlatformID: "lookup", SessionList: []db.Session{{ID: "s1", Directory: "/repo"}}, SessionFn: func(id string) (*platforms.SessionDetail, error) {
		if id != "s1" {
			t.Fatalf("wrong lookup target: %s", id)
		}
		resolved = true
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo"}}, nil
	}})
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pull_requests","arguments":{"action":"linked","directory":"/repo","platform":"lookup","remote_id":"local"}}}`))
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	mustRoutes(t, s).ServeHTTP(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"isError":true`) || !resolved || !strings.Contains(w.Body.String(), "pullRequests") {
		t.Fatalf("lookup did not dispatch in one call: %d %s", w.Code, w.Body.String())
	}
}
