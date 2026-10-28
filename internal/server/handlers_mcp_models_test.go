package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestMCPModelsRoutesToTargetPlatform(t *testing.T) {
	srv := testServer(t)
	called := false
	srv.registry.Register(&fakePlatform{id: "r-owner:opencode", directoryCatalogFn: func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		called = true
		if req.Directory != "/remote/repo" || req.Port != "" {
			t.Fatalf("request = %#v", req)
		}
		return &platforms.DirectoryCatalog{LiveConnection: true, Models: &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{{Provider: "remote", Model: "model", IsAvailable: true}}}}, nil
	}})
	handler := srv.buildMCPHandler()
	for _, tc := range []struct {
		platform string
		want     string
	}{
		{"r-owner:opencode", `\"provider\": \"remote\"`},
		{"r-disconnected:opencode", "model catalog request failed"},
	} {
		called = false
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"models","arguments":{"directory":"/remote/repo","platform":"`+tc.platform+`"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) || called != (tc.platform == "r-owner:opencode") {
			t.Fatalf("platform %s: called=%v status=%d body=%s", tc.platform, called, rec.Code, rec.Body.String())
		}
	}
}
