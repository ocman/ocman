package relayauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/share"
)

type harness struct {
	s   *Service
	mux *http.ServeMux
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{now: time.Now()}
	h.s, err = New(Config{PublicURL: "https://relay.example", Store: store, Now: func() time.Time { return h.now }, Providers: []Provider{
		{ID: "github", Kind: "github", Name: "GitHub", ClientID: "gh-id", ClientSecret: "gh-secret"},
		{ID: "google", Kind: "google", Name: "Google", ClientID: "g-id", ClientSecret: "g-secret"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	h.mux = http.NewServeMux()
	h.s.Routes(h.mux)
	return h
}

func (h *harness) request(method, path, body, bearer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

func (h *harness) connect(t *testing.T) (string, string) {
	t.Helper()
	w := h.request("POST", "/auth/connect", "", "")
	var result struct {
		AuthorizationURL string `json:"authorizationUrl"`
		PollToken        string `json:"pollToken"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	u, _ := url.Parse(result.AuthorizationURL)
	return strings.TrimPrefix(u.Path, "/auth/connect/"), result.PollToken
}

func TestProviderSignInAndEnrollment(t *testing.T) {
	for _, kind := range []string{"github", "google"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			var verifier string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_ = r.ParseForm()
					verifier = r.PostForm.Get("code_verifier")
					if verifier == "" || r.PostForm.Get("redirect_uri") != "https://relay.example/auth/callback/"+kind || r.PostForm.Get("client_secret") == "" {
						t.Error("missing exchange binding")
					}
					_, _ = io.WriteString(w, `{"access_token":"provider-secret"}`)
				case "/user":
					if r.Header.Get("Authorization") != "Bearer provider-secret" {
						t.Error("userinfo authorization missing")
					}
					if kind == "github" {
						_, _ = io.WriteString(w, `{"id":12345}`)
					} else {
						_, _ = io.WriteString(w, `{"sub":"12345"}`)
					}
				}
			}))
			defer provider.Close()
			h.s.endpoints[kind] = endpoints{provider.URL + "/authorize", provider.URL + "/token", provider.URL + "/user"}
			id, poll := h.connect(t)
			choices := h.request("GET", "/auth/connect/"+id, "", "")
			if !strings.Contains(choices.Body.String(), "Continue with GitHub") || !strings.Contains(choices.Body.String(), "Continue with Google") || strings.Contains(choices.Body.String(), "gh-secret") {
				t.Fatalf("choices: %s", choices.Body)
			}
			list := h.request("GET", "/auth/providers", "", "")
			if strings.Contains(list.Body.String(), "clientId") || strings.Contains(list.Body.String(), "secret") {
				t.Fatal("provider credentials leaked")
			}
			if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusAccepted {
				t.Fatal("not pending")
			}
			start := h.request("GET", "/auth/login/"+id+"/"+kind, "", "")
			if start.Code != http.StatusSeeOther {
				t.Fatalf("start: %d %s", start.Code, start.Body)
			}
			u, _ := url.Parse(start.Header().Get("Location"))
			state := u.Query().Get("state")
			cookie := start.Result().Cookies()[0]
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("unsafe cookie")
			}
			callback := "/auth/callback/" + kind + "?state=" + state + "&code=code"
			if w := h.request("GET", callback, "", ""); w.Code != http.StatusBadRequest {
				t.Fatal("unbound callback accepted")
			}
			w := h.request("GET", callback, "", "", cookie)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Connect ocman</button>") || digest(verifier) != u.Query().Get("code_challenge") {
				t.Fatalf("callback: %d %s", w.Code, w.Body)
			}
			if w := h.request("GET", callback, "", "", cookie); w.Code != http.StatusBadRequest {
				t.Fatal("callback replay accepted")
			}
			if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusAccepted {
				t.Fatal("enrollment issued without approval")
			}
			if w := h.request("POST", "/auth/approve", "state="+state, ""); w.Code != http.StatusBadRequest {
				t.Fatal("unbound approval accepted")
			}
			if w := h.request("POST", "/auth/approve", "state="+state, "", cookie); w.Code != http.StatusOK {
				t.Fatalf("approval: %d %s", w.Code, w.Body)
			}
			w = h.request("POST", "/auth/poll", "", poll)
			var result struct {
				Token string `json:"enrollmentToken"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Token == "" {
				t.Fatalf("poll: %d %s", w.Code, w.Body)
			}
			if !h.s.Authorize(t.Context(), result.Token) || h.s.Authorize(t.Context(), poll) || h.s.Authorize(t.Context(), "") {
				t.Fatal("enrollment validation")
			}
			b, _ := h.s.cfg.Store.Get(t.Context(), credentialKey(result.Token))
			if strings.Contains(string(b), result.Token) || strings.Contains(string(b), "provider-secret") || !strings.Contains(string(b), digest(kind+":12345")) {
				t.Fatalf("stored credentials: %s", b)
			}
			// Persisted tokens survive process restarts and remain revocable individually.
			restarted, _ := New(h.s.cfg)
			if !restarted.Authorize(t.Context(), result.Token) {
				t.Fatal("credential lost on restart")
			}
			if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusUnauthorized {
				t.Fatal("poll replay accepted")
			}
			if w := h.request("DELETE", "/auth/enrollment", "", result.Token); w.Code != http.StatusNoContent {
				t.Fatalf("revoke: %d", w.Code)
			}
			if h.s.Authorize(t.Context(), result.Token) {
				t.Fatal("revoked credential accepted")
			}
		})
	}
}

func TestExpiredAndInvalidConnections(t *testing.T) {
	h := newHarness(t)
	id, poll := h.connect(t)
	for _, path := range []string{"/auth/login/" + id + "/missing", "/auth/login/missing/github", "/auth/callback/github?state=invalid&code=x"} {
		if w := h.request("GET", path, "", ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if w := h.request("POST", "/auth/poll", "", "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatal("invalid poll accepted")
	}
	h.now = h.now.Add(connectTTL)
	if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusUnauthorized {
		t.Fatal("expired poll accepted")
	}
	if w := h.request("GET", "/auth/connect/"+id, "", ""); w.Code != http.StatusGone {
		t.Fatal("expired browser connection accepted")
	}
	if w := h.request("DELETE", "/auth/enrollment", "", "invalid"); w.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized revocation accepted")
	}
}

func TestConfigValidation(t *testing.T) {
	h := newHarness(t)
	for _, raw := range []string{"", "http://relay.example", "https://user:pass@relay.example", "https://relay.example/path", "https://relay.example?query=1"} {
		cfg := h.s.cfg
		cfg.PublicURL = raw
		if _, err := New(cfg); err == nil {
			t.Errorf("accepted public URL %q", raw)
		}
	}
	for _, p := range []Provider{{}, {ID: "github", Kind: "unknown", Name: "Test", ClientID: "x", ClientSecret: "x"}} {
		cfg := h.s.cfg
		cfg.Providers = []Provider{p}
		if _, err := New(cfg); err == nil {
			t.Error("accepted invalid provider")
		}
	}
	cfg := h.s.cfg
	cfg.Providers = append(cfg.Providers, cfg.Providers[0])
	if _, err := New(cfg); err == nil {
		t.Error("accepted duplicate provider")
	}
}
