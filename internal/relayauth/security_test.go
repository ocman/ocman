package relayauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOAuthProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name, token, user       string
		tokenStatus, userStatus int
	}{
		{"token rejected", `{}`, `{}`, 401, 200},
		{"malformed token", `invalid`, `{}`, 200, 200},
		{"missing access token", `{}`, `{}`, 200, 200},
		{"userinfo rejected", `{"access_token":"secret"}`, `{}`, 200, 403},
		{"missing identity", `{"access_token":"secret"}`, `{}`, 200, 200},
		{"malformed identity", `{"access_token":"secret"}`, `invalid`, 200, 200},
		{"redirect rejected", `{}`, `{}`, 302, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					w.Header().Set("Location", "/user")
					w.WriteHeader(tc.tokenStatus)
					_, _ = w.Write([]byte(tc.token))
					return
				}
				w.WriteHeader(tc.userStatus)
				_, _ = w.Write([]byte(tc.user))
			}))
			defer provider.Close()
			h.s.endpoints["github"] = endpoints{provider.URL, provider.URL + "/token", provider.URL + "/user"}
			if _, err := h.s.identity(t.Context(), h.s.cfg.Providers[0], "verifier", "code"); err == nil {
				t.Fatal("invalid provider identity accepted")
			}
		})
	}
}

func TestLoginBindingsAndExpiry(t *testing.T) {
	h := newHarness(t)
	id, _ := h.connect(t)
	start := h.request("GET", "/auth/login/"+id+"/github", "", "")
	cookie := start.Result().Cookies()[0]
	state := cookie.Value
	if w := h.request("GET", "/auth/callback/google?state="+state+"&code=x", "", "", cookie); w.Code != http.StatusBadRequest {
		t.Fatal("provider substitution accepted")
	}
	if w := h.request("POST", "/auth/approve", "state="+state, "", cookie); w.Code != http.StatusNotFound {
		t.Fatal("removed approval endpoint still available")
	}
	h.now = h.now.Add(connectTTL)
	if w := h.request("GET", "/auth/callback/github?state="+state+"&code=x", "", "", cookie); w.Code != http.StatusBadRequest {
		t.Fatal("expired callback accepted")
	}
}

func TestSignInLimits(t *testing.T) {
	h := newHarness(t)
	for range 10 {
		h.connect(t)
	}
	if w := h.request("POST", "/auth/connect", "", ""); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("unlimited connections: %d", w.Code)
	}
	h.now = h.now.Add(time.Minute)
	h.connect(t)
	for i := range maxPending {
		h.s.connections[string(rune(i))] = &connection{expires: h.now.Add(connectTTL)}
	}
	w := httptest.NewRecorder()
	h.s.connect(w, httptest.NewRequest(http.MethodPost, "/auth/connect", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("pending connection capacity not bounded")
	}
}

type failingStore struct{ Store }

func (failingStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("unavailable")
}
func (failingStore) Put(context.Context, string, []byte) error  { return errors.New("unavailable") }
func (failingStore) DeletePrefix(context.Context, string) error { return errors.New("unavailable") }

func TestCredentialExpiryAndStorageFailures(t *testing.T) {
	h := newHarness(t)
	token := randomToken()
	b, _ := json.Marshal(credential{User: "user", ExpiresAt: h.now.Add(time.Hour).UnixMilli()})
	if err := h.s.cfg.Store.Put(t.Context(), credentialKey(token), b); err != nil {
		t.Fatal(err)
	}
	if !h.s.Authorize(t.Context(), token) {
		t.Fatal("valid credential rejected")
	}
	h.now = h.now.Add(time.Hour)
	if h.s.Authorize(t.Context(), token) {
		t.Fatal("expired credential accepted")
	}
	h.s.cfg.Store = failingStore{h.s.cfg.Store}
	if h.s.Authorize(t.Context(), token) {
		t.Fatal("storage failure authorized")
	}
}

func TestCallbackStorageFailureDoesNotCompleteConnection(t *testing.T) {
	h := newHarness(t)
	id, poll := h.connect(t)
	state := randomToken()
	h.s.logins[state] = &login{connection: id, provider: "github", verifier: "verifier", expires: h.now.Add(connectTTL)}
	useSuccessfulProvider(t, h)
	h.s.cfg.Store = failingStore{h.s.cfg.Store}
	w := h.request("GET", "/auth/callback/github?state="+state+"&code=code", "", "", &http.Cookie{Name: "relay_login", Value: state})
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "user") {
		t.Fatalf("storage failure: %d %s", w.Code, w.Body)
	}
	if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusAccepted {
		t.Fatal("failed enrollment completed")
	}
}

func TestPendingLoginFailures(t *testing.T) {
	h := newHarness(t)
	id, _ := h.connect(t)
	state := randomToken()
	cookie := &http.Cookie{Name: "relay_login", Value: state}
	h.s.logins[state] = &login{connection: id, provider: "github", expires: h.now.Add(connectTTL)}
	if w := h.request("GET", "/auth/callback/github?state="+state+"&code=used", "", "", cookie); w.Code != http.StatusBadRequest {
		t.Fatal("consumed verifier accepted")
	}
	h.s.logins[state].verifier = "verifier"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private-provider-error", http.StatusForbidden)
	}))
	defer provider.Close()
	h.s.endpoints["github"] = endpoints{provider.URL, provider.URL, provider.URL}
	w := h.request("GET", "/auth/callback/github?state="+state+"&code=bad", "", "", cookie)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "private-provider-error") {
		t.Fatalf("provider failure: %d %s", w.Code, w.Body)
	}
	h.s.logins[state] = &login{connection: id, provider: "github", verifier: "verifier", expires: h.now.Add(connectTTL)}
	useSuccessfulProvider(t, h)
	h.s.connections[id].token = "already-completed"
	if w := h.request("GET", "/auth/callback/github?state="+state+"&code=code", "", "", cookie); w.Code != http.StatusGone {
		t.Fatal("completed connection enrolled again")
	}
	for i := range maxPending {
		h.s.logins[string(rune(i))] = &login{expires: h.now.Add(connectTTL)}
	}
	h.s.connections[id].token = ""
	if w := h.request("GET", "/auth/login/"+id+"/github", "", ""); w.Code != http.StatusTooManyRequests {
		t.Fatal("login capacity not bounded")
	}
}

type failingDeleteStore struct{ Store }

func (failingDeleteStore) DeletePrefix(context.Context, string) error {
	return errors.New("unavailable")
}

func TestRevocationStorageFailure(t *testing.T) {
	h := newHarness(t)
	token := randomToken()
	b, _ := json.Marshal(credential{User: "user", ExpiresAt: h.now.Add(time.Hour).UnixMilli()})
	if err := h.s.cfg.Store.Put(t.Context(), credentialKey(token), b); err != nil {
		t.Fatal(err)
	}
	h.s.cfg.Store = failingDeleteStore{h.s.cfg.Store}
	if w := h.request("DELETE", "/auth/enrollment", "", token); w.Code != http.StatusInternalServerError {
		t.Fatal("failed revocation reported as success")
	}
	if bearer(httptest.NewRequest(http.MethodGet, "/", nil)) != "" {
		t.Fatal("absent bearer accepted")
	}
}

func TestLimiterProxyAndCapacity(t *testing.T) {
	h := newHarness(t)
	for _, window := range []requestWindow{{start: h.now}, {start: h.now.Add(-time.Minute)}} {
		h.s.limits = map[string]requestWindow{}
		for i := range 10000 {
			h.s.limits[string(rune(i))] = window
		}
		w := h.request("GET", "/auth/providers", "", "")
		want := http.StatusTooManyRequests
		if window.start.Before(h.now) {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("capacity: %d, want %d", w.Code, want)
		}
	}
	h.s.cfg.TrustProxy = true
	for _, forwarded := range []string{"198.51.100.1", "invalid"} {
		r := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
		r.RemoteAddr = "peer-without-port"
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("forwarded address: %d", w.Code)
		}
	}
}

func TestConfigDefaultsAndTransportFailure(t *testing.T) {
	h := newHarness(t)
	cfg := h.s.cfg
	cfg.Now, cfg.HTTP = nil, nil
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.Store = nil }, func(c *Config) { c.Providers = nil }, func(c *Config) { c.PublicURL = "https://%invalid" }} {
		copyCfg := cfg
		change(&copyCfg)
		if _, err := New(copyCfg); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	provider.Close()
	h.s.endpoints["github"] = endpoints{provider.URL, provider.URL, provider.URL}
	if _, err := h.s.identity(t.Context(), cfg.Providers[0], "verifier", "code"); err == nil {
		t.Fatal("unreachable provider authenticated")
	}
}
