package relayauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelaySignInPagesUseSharedStyles(t *testing.T) {
	h := newHarness(t)
	id, _ := h.connect(t)
	w := h.request(http.MethodGet, "/auth/connect/"+id, "", "")
	if !strings.Contains(w.Body.String(), `href="/auth/assets/style.css"`) || !strings.Contains(w.Body.String(), `<main`) {
		t.Fatalf("sign-in page is missing its styled layout: %s", w.Body)
	}
	for _, asset := range []struct{ name, contentType string }{{"style.css", "text/css"}, {"logo.svg", "image/svg+xml"}} {
		w := h.request(http.MethodGet, "/auth/assets/"+asset.name, "", "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), asset.contentType) {
			t.Errorf("asset %s: %d %s", asset.name, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := h.request(http.MethodGet, "/auth/assets/missing", "", ""); w.Code != http.StatusNotFound {
		t.Fatal("unknown asset did not return 404")
	}
}

func TestProviderFailureRendersRetryPageInsteadOfGatewayError(t *testing.T) {
	h := newHarness(t)
	id, _ := h.connect(t)
	state := randomToken()
	h.s.logins[state] = &login{connection: id, provider: "github", verifier: "private-verifier", expires: h.now.Add(connectTTL)}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private-provider-error", http.StatusUnauthorized)
	}))
	defer provider.Close()
	h.s.endpoints["github"] = endpoints{provider.URL, provider.URL, provider.URL}
	w := h.request(http.MethodGet, "/auth/callback/github?state="+state+"&code=private-code", "", "", &http.Cookie{Name: "relay_login", Value: state})
	if w.Code != http.StatusBadRequest || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "Sign-in unavailable") {
		t.Fatalf("expected a retry page, not a gateway error: %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{"private-provider-error", "private-verifier", "private-code"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("failure page leaked %s", secret)
		}
	}
	if !strings.Contains(w.Body.String(), "/auth/connect/"+id) {
		t.Fatal("missing safe retry link")
	}
	if h.s.logins[state] != nil || h.s.connections[id].token != "" {
		t.Fatal("failed login retained or enrollment issued")
	}
}
