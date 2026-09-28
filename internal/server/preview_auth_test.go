package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
)

// mockOAuth is a confidential OAuth provider that enforces PKCE, the exact
// redirect URI and the server-side client secret.
type mockOAuth struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	challenge map[string]string // code -> S256 challenge
	issued    int32
	refreshes int32
	revoked   []string
}

func newMockOAuth(t *testing.T) *mockOAuth {
	m := &mockOAuth{t: t, challenge: map[string]string{}}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockOAuth) provider() previewauth.Provider {
	return previewauth.Provider{
		ID: "mock", Name: "Mock", AuthURL: m.srv.URL + "/authorize", TokenURL: m.srv.URL + "/token",
		RevokeURL: m.srv.URL + "/revoke", ClientID: "cid", ClientSecret: "shh-secret", Scopes: []string{"read"}, PKCE: true,
		Identify: func(_ context.Context, _ *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			return []previewauth.Grant{
				{WorkspaceID: "w1", WorkspaceName: "Acme", AccountName: "ann", Sites: []state.PreviewSite{{ID: "s1", Name: "Acme Site"}}},
				{WorkspaceID: "w2", WorkspaceName: "Beta", AccountName: "ann"},
			}, nil
		},
	}
}

func (m *mockOAuth) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	switch r.URL.Path {
	case "/revoke":
		m.mu.Lock()
		m.revoked = append(m.revoked, r.PostForm.Get("token"))
		m.mu.Unlock()
		return
	case "/token":
	default:
		http.NotFound(w, r)
		return
	}
	if r.PostForm.Get("client_secret") != "shh-secret" || r.PostForm.Get("client_id") != "cid" {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		m.mu.Lock()
		want, ok := m.challenge[r.PostForm.Get("code")]
		m.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != want ||
			r.PostForm.Get("redirect_uri") != "http://127.0.0.1:0"+previewCallback {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		n := atomic.AddInt32(&m.issued, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + fmt.Sprint(n), "refresh_token": "refresh-" + fmt.Sprint(n), "expires_in": 1})
	case "refresh_token":
		atomic.AddInt32(&m.refreshes, 1)
		time.Sleep(50 * time.Millisecond)
		if r.PostForm.Get("refresh_token") == "dead" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "rotated-" + r.PostForm.Get("refresh_token"), "refresh_token": "refresh-rotated", "expires_in": 3600})
	}
}

// authorize simulates the provider consent screen: it records the PKCE
// challenge for a fresh code and returns the callback URL.
func (m *mockOAuth) authorize(t *testing.T, authorizeURL, code string) string {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "cid" || strings.Contains(authorizeURL, "shh-secret") {
		t.Fatalf("bad authorize URL %s", authorizeURL)
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:0"+previewCallback {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	m.mu.Lock()
	m.challenge[code] = q.Get("code_challenge")
	m.mu.Unlock()
	return previewCallback + "?" + url.Values{"state": {q.Get("state")}, "code": {code}}.Encode()
}

type browser struct{ cookies map[string]string }

func (b *browser) do(t *testing.T, s *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "localhost:8228"
	for k, v := range b.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return rr
}

func newBrowser() *browser { return &browser{cookies: map[string]string{}} }

func (b *browser) connect(t *testing.T, s *Server, m *mockOAuth, code, returnTo string) string {
	t.Helper()
	rr := b.do(t, s, http.MethodPost, "/api/previews/connect", `{"provider":"mock","returnTo":"`+returnTo+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rr.Code, rr.Body.String())
	}
	var resp struct{ AuthorizeURL string }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return m.authorize(t, resp.AuthorizeURL, code)
}

func previewServer(t *testing.T, m *mockOAuth, statePath string) *Server {
	t.Helper()
	s := testServer(t)
	if statePath != "" {
		db, err := state.Open(statePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		s.stateDB = db
	}
	return s.WithPreviewProviders(nil, m.provider())
}

func statusOf(t *testing.T, b *browser, s *Server) string {
	rr := b.do(t, s, http.MethodGet, "/api/previews/providers", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// OAuth grants belong to the machine: any browser with app access sees
// and uses them, and signing out does not forget them.
func TestPreviewAuth_ConnectIsMachineWide(t *testing.T) {
	m := newMockOAuth(t)
	path := filepath.Join(t.TempDir(), "state.db")
	s := previewServer(t, m, path)
	laptop, phone := newBrowser(), newBrowser()

	cb := laptop.connect(t, s, m, "code-a", "/settings?tab=links")
	rr := laptop.do(t, s, http.MethodGet, cb, "")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/settings?previewAuth=connected&tab=links" {
		t.Fatalf("callback = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	body := statusOf(t, phone, s)
	if !strings.Contains(body, `"workspaceName":"Acme"`) || !strings.Contains(body, `"workspaceName":"Beta"`) || !strings.Contains(body, "Acme Site") {
		t.Fatalf("other browser misses the machine grant: %s", body)
	}
	if strings.Contains(body, "access-1") || strings.Contains(body, "refresh-1") || strings.Contains(body, "shh") {
		t.Fatalf("status leaks secrets: %s", body)
	}
	if len(laptop.cookies) != 0 {
		t.Fatalf("browser identity cookie set: %v", laptop.cookies)
	}

	// Replaying the consumed state fails, and does not redirect.
	if rr := laptop.do(t, s, http.MethodGet, cb, ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("replay = %d", rr.Code)
	}

	// Tokens are sealed at rest.
	var raw []byte
	sqlDB := openRaw(t, path)
	if err := sqlDB.QueryRow(`SELECT access_enc FROM preview_credential LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "access-") {
		t.Fatal("access token stored in plaintext")
	}

	// Disconnecting one workspace keeps the other and revokes at the provider.
	if rr := phone.do(t, s, http.MethodPost, "/api/previews/disconnect", `{"provider":"mock","workspaceId":"w1"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("disconnect = %d", rr.Code)
	}
	if body := statusOf(t, laptop, s); strings.Contains(body, "Acme") || !strings.Contains(body, "Beta") {
		t.Fatalf("after disconnect: %s", body)
	}
	if len(m.revoked) != 1 {
		t.Fatalf("revoked = %v", m.revoked)
	}

	// Signing out is about the browser session, not the machine's grants.
	if rr := laptop.do(t, s, http.MethodPost, "/api/auth/logout", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rr.Code)
	}
	if body := statusOf(t, phone, s); !strings.Contains(body, "Beta") {
		t.Fatalf("grant lost on sign-out: %s", body)
	}
}

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPreviewAuth_CallbackRejectsForgedState(t *testing.T) {
	s := previewServer(t, newMockOAuth(t), "")
	b := newBrowser()
	for _, target := range []string{previewCallback + "?code=x", previewCallback + "?state=forged&code=x"} {
		if rr := b.do(t, s, http.MethodGet, target, ""); rr.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", target, rr.Code)
		}
	}
	if body := statusOf(t, b, s); strings.Contains(body, "Acme") {
		t.Fatalf("forged state stored a grant: %s", body)
	}
}

func TestPreviewAuth_NoOpenRedirect(t *testing.T) {
	for _, evil := range []string{"//evil.example", "https://evil.example/x", `/\\evil.example`, "javascript:alert(1)"} {
		m := newMockOAuth(t)
		s := previewServer(t, m, "")
		b := newBrowser()
		cb := b.connect(t, s, m, "code-r", evil)
		rr := b.do(t, s, http.MethodGet, cb, "")
		if loc := rr.Header().Get("Location"); loc != "/?previewAuth=connected" {
			t.Fatalf("returnTo %q redirected to %q", evil, loc)
		}
	}
}

func TestPreviewAuth_SurvivesRestart(t *testing.T) {
	m := newMockOAuth(t)
	path := filepath.Join(t.TempDir(), "state.db")
	b := newBrowser()
	s1 := previewServer(t, m, path)
	cb := b.connect(t, s1, m, "code-s", "/")
	s1.stateDB.Close()

	s2 := previewServer(t, m, path)
	if rr := b.do(t, s2, http.MethodGet, cb, ""); rr.Code != http.StatusSeeOther {
		t.Fatalf("callback after restart = %d %s", rr.Code, rr.Body.String())
	}
	if body := statusOf(t, b, s2); !strings.Contains(body, "Acme") {
		t.Fatalf("connection lost: %s", body)
	}
}

func TestPreviewAuth_OwnerAndAccessFailClosed(t *testing.T) {
	m := newMockOAuth(t)
	s := previewServer(t, m, "")
	b := newBrowser()
	if rr := b.do(t, s, http.MethodGet, "/api/previews/providers?remoteId=someone-else", ""); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("foreign owner = %d", rr.Code)
	}
	if rr := b.do(t, s, http.MethodGet, "/api/previews/providers?remoteId=local", ""); rr.Code != http.StatusOK {
		t.Fatalf("local owner = %d", rr.Code)
	}

	// Auth off: only direct loopback clients reach private preview routes.
	mux, _ := s.routes()
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.4:1234" },
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.4") },
		func(r *http.Request) { r.Host = "ocman.example.com" },
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/previews/providers", nil)
		req.RemoteAddr, req.Host = "127.0.0.1:1", "localhost:8228"
		mutate(req)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("remote unauthenticated = %d", rr.Code)
		}
	}

	// Auth on: a remote client needs the app cookie.
	s.auth = newTestAuth(t, "pw")
	req := httptest.NewRequest(http.MethodGet, "/api/previews/providers", nil)
	req.RemoteAddr = "192.0.2.4:1234"
	rr := httptest.NewRecorder()
	mux, _ = s.routes()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth on, no cookie = %d", rr.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/previews/providers", nil)
	req.RemoteAddr = "192.0.2.4:1234"
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: s.auth.signToken(time.Now().Add(time.Hour))})
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("auth on, cookie = %d", rr.Code)
	}
}

// OAuth apps from the environment register their provider with consent;
// an app alone does not configure previews until a grant exists.
func TestPreviewAuth_AppsFromEnv(t *testing.T) {
	for id, env := range map[string][]string{
		"slack":  {"OCMAN_SLACK_PREVIEW_CLIENT_ID", "OCMAN_SLACK_PREVIEW_CLIENT_SECRET"},
		"notion": {"OCMAN_NOTION_PREVIEW_CLIENT_ID", "OCMAN_NOTION_PREVIEW_CLIENT_SECRET"},
		"linear": {"OCMAN_LINEAR_PREVIEW_CLIENT_ID"},
		"jira":   {"OCMAN_JIRA_PREVIEW_CLIENT_ID", "OCMAN_JIRA_PREVIEW_CLIENT_SECRET"},
	} {
		t.Run(id, func(t *testing.T) {
			for _, k := range env {
				t.Setenv(k, "v")
			}
			s := testServer(t)
			if p, ok := s.previewManager().Provider(id); !ok || !p.OAuth() {
				t.Fatalf("%s provider = %+v %v", id, p, ok)
			}
			for _, e := range catalogOf(t, s) {
				if e.ID == id && (e.Configured || !e.OAuth) {
					t.Fatalf("%s entry = %+v", id, e)
				}
			}
		})
	}
	s := testServer(t)
	for _, id := range []string{"slack", "jira"} {
		if _, ok := s.previewManager().Provider(id); ok {
			t.Fatalf("%s registered without an app", id)
		}
	}
}
