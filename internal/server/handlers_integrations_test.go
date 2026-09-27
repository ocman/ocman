package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
)

// registerForgejoClient wires a forgejo client pointed at api into the
// server's integration registry under host "code.example.com". The pasted
// "web" URLs in the tests use that host; the client's baseURL is the
// httptest server so requests stay in-process.
func registerForgejoClient(srv *Server, host, apiBase string) {
	client := forgejo.NewForTest(host, apiBase, "tok", http.DefaultClient)
	srv.integrations.Forgejo = forgejo.NewRegistryForTest(map[string]*forgejo.Client{
		host: client,
	})
}

func TestHandleIntegrationsStatus_ReportsForgejoHosts(t *testing.T) {
	srv := testServer(t)
	registerForgejoClient(srv, "code.example.com", "http://unused")

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/status", nil)
	rr := httptest.NewRecorder()
	srv.handleIntegrationsStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"forgejo"`) || !strings.Contains(body, "code.example.com") {
		t.Errorf("expected forgejo hosts in status payload, got %s", body)
	}
}
