package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/integrations/forgejo"
)

// fakeForgejoServer mounts the minimal API endpoints needed to test the
// Forgejo preview handler. Returns the httptest server plus a host string
// the caller registers in srv.integrations.Forgejo.
func fakeForgejoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/myproj/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Patch","state":"open","html_url":"x","user":{"login":"alice"}}`))
		case "/api/v1/repos/alice/myproj/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Bug","state":"closed","user":{"login":"alice"}}`))
		case "/api/v1/repos/alice/myproj/git/commits/abc1234":
			_, _ = w.Write([]byte(`{"sha":"abc1234","commit":{"message":"do thing"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

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

func TestHandleForgejoPreview_PR(t *testing.T) {
	srv := testServer(t)
	api := fakeForgejoServer(t)
	registerForgejoClient(srv, "code.example.com", api.URL)

	url := "https://code.example.com/alice/myproj/pulls/7"
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/forgejo/preview?url="+url, nil)
	rr := httptest.NewRecorder()
	srv.handleForgejoPreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["title"] != "Patch" || data["state"] != "open" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestHandleForgejoPreview_Issue(t *testing.T) {
	srv := testServer(t)
	api := fakeForgejoServer(t)
	registerForgejoClient(srv, "code.example.com", api.URL)

	url := "https://code.example.com/alice/myproj/issues/3"
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/forgejo/preview?url="+url, nil)
	rr := httptest.NewRecorder()
	srv.handleForgejoPreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &data)
	if data["title"] != "Bug" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestHandleForgejoPreview_Commit(t *testing.T) {
	srv := testServer(t)
	api := fakeForgejoServer(t)
	registerForgejoClient(srv, "code.example.com", api.URL)

	url := "https://code.example.com/alice/myproj/commit/abc1234"
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/forgejo/preview?url="+url, nil)
	rr := httptest.NewRecorder()
	srv.handleForgejoPreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var data map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &data)
	if data["sha"] != "abc1234" {
		t.Errorf("unexpected payload: %+v", data)
	}
}

func TestHandleForgejoPreview_UnknownHostRejected(t *testing.T) {
	srv := testServer(t)
	api := fakeForgejoServer(t)
	registerForgejoClient(srv, "code.example.com", api.URL)

	// Host not in the registry => 422, never proxied.
	url := "https://evil.example.org/alice/myproj/pulls/7"
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/forgejo/preview?url="+url, nil)
	rr := httptest.NewRecorder()
	srv.handleForgejoPreview(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown host, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleForgejoPreview_MissingURL(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/forgejo/preview", nil)
	rr := httptest.NewRecorder()
	srv.handleForgejoPreview(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
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
