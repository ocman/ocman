package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

)

type appsResp struct {
	Apps []previewAppView
}

func appsOf(t *testing.T, rr *httptest.ResponseRecorder) map[string]previewAppView {
	t.Helper()
	var resp appsResp
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil {
		t.Fatalf("apps = %d %s", rr.Code, rr.Body.String())
	}
	out := map[string]previewAppView{}
	for _, a := range resp.Apps {
		out[a.ID] = a
	}
	return out
}

func TestPreviewApps_SavedFromSettingsOverrideEnv(t *testing.T) {
	t.Setenv("OCMAN_GITHUB_PREVIEW_CLIENT_ID", "env-id")
	t.Setenv("OCMAN_GITHUB_PREVIEW_CLIENT_SECRET", "env-secret")
	s := testServer(t)
	b := newBrowser()

	if p, _ := s.previewManager().Provider("github"); p.ClientID != "env-id" {
		t.Fatalf("env app = %q", p.ClientID)
	}
	if a := appsOf(t, b.do(t, s, http.MethodGet, "/api/previews/apps", ""))["github"]; a.Source != "env" || !a.InEnv || !a.HasSecret {
		t.Fatalf("env view = %+v", a)
	}

	// Settings wins over the environment, and rebuilds the providers.
	rr := b.do(t, s, http.MethodPost, "/api/previews/apps/save", `{"kind":"github","clientId":" ui-id ","clientSecret":"ui-secret"}`)
	if a := appsOf(t, rr)["github"]; a.Source != "settings" || !a.InEnv || a.ClientID != "ui-id" {
		t.Fatalf("saved view = %+v", a)
	}
	if strings.Contains(rr.Body.String(), "ui-secret") {
		t.Fatalf("response leaks secret: %s", rr.Body.String())
	}
	if p, _ := s.previewManager().Provider("github"); p.ClientID != "ui-id" || p.ClientSecret != "ui-secret" {
		t.Fatalf("override not applied: %s", p.ClientID)
	}

	// An empty secret keeps the saved one.
	b.do(t, s, http.MethodPost, "/api/previews/apps/save", `{"kind":"github","clientId":"ui-id-2"}`)
	if p, _ := s.previewManager().Provider("github"); p.ClientID != "ui-id-2" || p.ClientSecret != "ui-secret" {
		t.Fatalf("secret not kept: %s", p.ClientID)
	}

	// Removing the saved app falls back to the environment's.
	b.do(t, s, http.MethodPost, "/api/previews/apps/remove", `{"id":"github"}`)
	if p, _ := s.previewManager().Provider("github"); p.ClientID != "env-id" {
		t.Fatalf("fallback = %q", p.ClientID)
	}
	raw, _, _ := s.stateDB.GetSetting(t.Context(), previewAppsKey)
	if strings.Contains(raw, "ui-secret") {
		t.Fatal("apps stored in plaintext")
	}
}

func TestPreviewApps_HostedAndValidation(t *testing.T) {
	s := testServer(t)
	b := newBrowser()
	for _, body := range []string{
		`{"kind":"nope","clientId":"x","clientSecret":"y"}`,
		`{"kind":"github","clientId":"","clientSecret":"y"}`,
		`{"kind":"github","clientId":"x"}`,
		`{"kind":"github","host":"x.com","clientId":"x","clientSecret":"y"}`,
		`{"kind":"forgejo","host":"bad host","clientId":"x","clientSecret":"y"}`,
		`{"kind":"forgejo","clientId":"x","clientSecret":"y"}`,
	} {
		if rr := b.do(t, s, http.MethodPost, "/api/previews/apps/save", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, rr.Code)
		}
	}
	b.do(t, s, http.MethodPost, "/api/previews/apps/save", `{"kind":"forgejo","host":"Code.Example.com","clientId":"f","clientSecret":"s"}`)
	b.do(t, s, http.MethodPost, "/api/previews/apps/save", `{"kind":"gitlab","host":"gitlab.com","clientId":"g"}`)
	b.do(t, s, http.MethodPost, "/api/previews/apps/save", `{"kind":"linear","clientId":"l"}`)
	m := s.previewManager()
	for _, id := range []string{"forgejo:code.example.com", "gitlab:gitlab.com", "linear"} {
		if _, ok := m.Provider(id); !ok {
			t.Fatalf("provider %s missing", id)
		}
	}
	// An app alone offers sign-in; links wait for a grant or token.
	if p, _ := m.Provider("forgejo:code.example.com"); !p.OAuth() {
		t.Fatal("saved app has no sign-in")
	}
	if refs := s.linkPreviews().Discover("https://code.example.com/a/b/pulls/2", nil); len(refs) != 0 {
		t.Fatalf("unconfigured host looked up: %v", refs)
	}
}

func TestPreviewApps_Forbidden(t *testing.T) {
	s := testServer(t)
	mux, _ := s.routes()
	for _, target := range []string{"/api/previews/apps", "/api/previews/apps/save", "/api/previews/apps/remove"} {
		method := http.MethodPost
		if target == "/api/previews/apps" {
			method = http.MethodGet
		}
		req := httptest.NewRequest(method, target, strings.NewReader(`{"kind":"linear","clientId":"l"}`))
		req.RemoteAddr, req.Host = "127.0.0.1:1", "localhost:8228"
		req.Header.Set("X-Forwarded-For", "192.0.2.4")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s proxied = %d", target, rr.Code)
		}
	}
	if p, _ := s.previewManager().Provider("linear"); p.OAuth() {
		t.Fatal("proxied save applied")
	}
}

