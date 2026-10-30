package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/httpx"
)

func TestBrowserPolicyBlocksAutomaticExternalImagesAndSockets(t *testing.T) {
	fixture, err := os.ReadFile("../../frontend/e2e/browser-security/headers.json")
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err := json.Unmarshal(fixture, &headers); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"localhost:8228", "ocman.example:443", "[::1]:8228"} {
		t.Run(host, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			r.Host = host
			r.Header.Set("X-Forwarded-Host", "attacker.invalid")
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			withSecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)
			policy := w.Header().Get("Content-Security-Policy")
			if expected := strings.ReplaceAll(headers["app"], "{{host}}", host); policy != expected {
				t.Fatalf("browser-test policy drift: %q != %q", policy, expected)
			}
			for _, required := range []string{
				"img-src 'self' data: blob:;",
				"connect-src 'self' ws://" + host + " wss://" + host + ";",
			} {
				if !strings.Contains(policy, required) {
					t.Errorf("policy does not contain %q: %s", required, policy)
				}
			}
			for _, forbidden := range []string{"ws: wss:", "http: https:", "attacker.invalid"} {
				if strings.Contains(policy, forbidden) {
					t.Errorf("policy contains broad or untrusted source %q", forbidden)
				}
			}
		})
	}
}

func TestBrowserPolicyRefusesInvalidAuthorities(t *testing.T) {
	for _, host := range []string{"", "localhost; connect-src *", "localhost/path", "user@localhost", "localhost?x", "localhost#x", "localhost:0", "localhost:"} {
		if got := httpx.BrowserSecurityPolicy(host); got != contentSecurityPolicy {
			t.Errorf("unsafe authority changed CSP: %q -> %s", host, got)
		}
	}
}
