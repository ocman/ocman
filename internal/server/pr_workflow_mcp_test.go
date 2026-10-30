package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
