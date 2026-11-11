package relayauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func useSuccessfulProvider(t *testing.T, h *harness) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"private-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":12345}`))
	}))
	t.Cleanup(provider.Close)
	h.s.endpoints["github"] = endpoints{provider.URL, provider.URL + "/token", provider.URL + "/user"}
}

func TestConnectionExpiresDuringProviderExchange(t *testing.T) {
	h := newHarness(t)
	id, poll := h.connect(t)
	state := randomToken()
	h.s.logins[state] = &login{connection: id, provider: "github", verifier: "verifier", expires: h.now.Add(connectTTL)}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"private-token"}`))
			return
		}
		// Expire the pending request while its identity lookup is in flight.
		h.now = h.now.Add(connectTTL)
		_, _ = w.Write([]byte(`{"id":12345}`))
	}))
	defer provider.Close()
	h.s.endpoints["github"] = endpoints{provider.URL, provider.URL + "/token", provider.URL + "/user"}
	w := h.request("GET", "/auth/callback/github?state="+state+"&code=code", "", "", &http.Cookie{Name: "relay_login", Value: state})
	if w.Code != http.StatusGone {
		t.Fatalf("expired callback: %d %s", w.Code, w.Body)
	}
	if w := h.request("POST", "/auth/poll", "", poll); w.Code != http.StatusUnauthorized {
		t.Fatal("expired connection issued enrollment")
	}
}
