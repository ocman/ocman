package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/previews"
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

func serve(s *Server, req *http.Request) *httptest.ResponseRecorder {
	mux, _ := s.routes()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func localRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr, req.Host = "127.0.0.1:5555", "localhost:8228"
	return req
}

func TestHandleIntegrationsStatus_ReportsForgejoHosts(t *testing.T) {
	srv := testServer(t)
	registerForgejoClient(srv, "code.example.com", "http://unused")
	srv.integrations.GitHub = github.NewForTest("https://api.github.com", "", nil)
	rr := serve(srv, localRequest(http.MethodGet, "/api/integrations/status", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"forgejo"`) || !strings.Contains(body, "code.example.com") || !strings.Contains(body, `"authenticated":false`) {
		t.Errorf("expected forgejo hosts in status payload, got %s", body)
	}
	srv.integrations.GitHub = nil // used to panic
	if rr := serve(srv, localRequest(http.MethodGet, "/api/integrations/status", "")); rr.Code != http.StatusOK {
		t.Fatalf("without a GitHub client: %d", rr.Code)
	}
}

// The hub's forge clients reach the previews module as credentials only:
// https Forgejo logins become configured hosts, a plain-http one never
// carries its token, and the GitHub CLI token is the owner fallback.
func TestPreviewForgesAdapter(t *testing.T) {
	s := testServer(t)
	s.integrations.GitHub = github.NewForTest("https://api.github.com", "tok-cli", nil)
	s.integrations.Forgejo = forgejo.NewRegistryForTest(map[string]*forgejo.Client{
		"tea.example.com":   forgejo.NewForTest("tea.example.com", "https://tea.example.com", "tok", nil),
		"plain.example.com": forgejo.NewForTest("plain.example.com", "http://plain.example.com", "tok", nil),
	})
	rr := serve(s, localRequest(http.MethodGet, "/api/previews/providers", ""))
	var resp struct {
		Providers []struct {
			ID, Source string
			Configured bool
		}
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || strings.Contains(rr.Body.String(), "tok-cli") || strings.Contains(rr.Body.String(), `"tok"`) {
		t.Fatalf("providers = %d %s", rr.Code, rr.Body.String())
	}
	got := map[string]string{}
	for _, p := range resp.Providers {
		if p.Configured {
			got[p.ID] = p.Source
		}
	}
	if got["github"] != "cli" || got["forgejo:tea.example.com"] != "cli" {
		t.Fatalf("configured = %v", got)
	}
	if _, ok := got["forgejo:plain.example.com"]; ok {
		t.Fatalf("plain-http host configured: %v", got)
	}
}

// The server supplies app access: direct loopback without auth, the auth
// cookie when auth is on, and never a proxied or foreign-host request.
func TestPreviewAccessAdapter(t *testing.T) {
	s := testServer(t)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.4:1234" },
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.4") },
		func(r *http.Request) { r.Header.Set("Forwarded", "for=192.0.2.4") },
		func(r *http.Request) { r.Host = "ocman.example.com" },
	} {
		req := localRequest(http.MethodGet, "/api/previews/providers", "")
		mutate(req)
		if rr := serve(s, req); rr.Code != http.StatusForbidden {
			t.Fatalf("not direct local = %d", rr.Code)
		}
	}
	if rr := serve(s, localRequest(http.MethodGet, "/api/previews/providers", "")); rr.Code != http.StatusOK {
		t.Fatalf("direct local = %d", rr.Code)
	}
	if rr := serve(s, localRequest(http.MethodPost, "/api/previews/providers", "")); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d", rr.Code)
	}

	s.auth = newTestAuth(t, "pw")
	req := httptest.NewRequest(http.MethodGet, "/api/previews/providers", nil)
	req.RemoteAddr = "192.0.2.4:1234"
	if rr := serve(s, req); rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth on, no cookie = %d", rr.Code)
	}
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: s.auth.signToken(time.Now().Add(time.Hour))})
	if rr := serve(s, req); rr.Code != http.StatusOK {
		t.Fatalf("auth on, cookie = %d", rr.Code)
	}
}

// Owner checks use the live remote router; credentials are the machine's,
// so signing out of the browser keeps them.
func TestPreviewOwnerAndSignOut(t *testing.T) {
	s := testServer(t)
	d := s.previewDeps()
	d.Providers = []previewauth.Provider{{ID: "mock", Name: "Mock", TokenHelp: "paste",
		Identify: func(context.Context, *http.Client, previewauth.Token) ([]previewauth.Grant, error) {
			return []previewauth.Grant{{WorkspaceID: "w1", WorkspaceName: "Acme"}}, nil
		}}}
	s.previewsMod.set(previews.NewModule(d))

	if rr := serve(s, localRequest(http.MethodGet, "/api/previews/providers?remoteId=r1", "")); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("disconnected remote = %d", rr.Code)
	}
	s.router().RegisterRemote("r1", s.router().Local())
	if rr := serve(s, localRequest(http.MethodGet, "/api/previews/providers?remoteId=r1", "")); rr.Code != http.StatusOK {
		t.Fatalf("connected remote = %d", rr.Code)
	}

	if rr := serve(s, localRequest(http.MethodPost, "/api/previews/token", `{"provider":"mock","token":"t"}`)); rr.Code != http.StatusNoContent {
		t.Fatalf("token = %d %s", rr.Code, rr.Body.String())
	}
	if rr := serve(s, localRequest(http.MethodPost, "/api/auth/logout", "")); rr.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rr.Code)
	}
	if rr := serve(s, localRequest(http.MethodGet, "/api/previews/providers", "")); !strings.Contains(rr.Body.String(), "Acme") {
		t.Fatalf("grant lost on sign-out: %s", rr.Body.String())
	}
}
