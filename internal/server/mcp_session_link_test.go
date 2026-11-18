package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func TestMCPCreateSessionLinkIsOriginRelative(t *testing.T) {
	for _, test := range []struct{ name, base, want string }{
		{"public", "https://ocman.example/dashboard/", "/session/ses-new?platform=opencode"},
		{"local", "", "/session/ses-new?platform=opencode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			srv.publicBaseURL, srv.addr, srv.mcpAddr = test.base, ":9123", "127.0.0.1:8227"
			srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:6620", RepoRoot: "/repo"}, nil
			}})
			reg.Register(&fakePlatform{PlatformID: "opencode",
				DirectoryCatalogFn: mcpTestCatalog("p/m"),
				CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					return &platforms.CreateSessionResponse{ID: "ses-new"}, nil
				},
				SendMessageFn: func(platforms.SendMessageRequest) error { return nil },
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sessions","arguments":{"action":"create","prompt":"go","platform":"opencode","model":"p/m","directory":"/repo","worktree":false}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			rec := httptest.NewRecorder()
			srv.buildMCPHandler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), test.want) || strings.Contains(rec.Body.String(), `"isError":true`) {
				t.Fatalf("status = %d, body = %s, want dashboard link %s", rec.Code, rec.Body.String(), test.want)
			}
			if strings.Contains(rec.Body.String(), "http://") || strings.Contains(rec.Body.String(), "https://") {
				t.Fatalf("session link includes an origin: %s", rec.Body.String())
			}
		})
	}
}
