package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayUsesRestrictedBrowserPolicy(t *testing.T) {
	h := newHarness(t, nil)
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://relay.example/", nil))
	policy := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "img-src 'self' data: blob:;") || !strings.Contains(policy, "connect-src 'self' ws://relay.example wss://relay.example;") {
		t.Fatalf("missing relay browser isolation: %s", policy)
	}
}
