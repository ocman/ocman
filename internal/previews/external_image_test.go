package previews

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
)

func TestExternalImageWrapper(t *testing.T) {
	mux := moduletest.Mount(&Module{}) // stateless; no DB or outbound HTTP client
	raw := `https://images.example/report.svg?x=" onerror="alert(1)&y=2`
	r := httptest.NewRequest(http.MethodGet, "/api/previews/external-image?url="+url.QueryEscape(raw), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
	}
	fixture, err := os.ReadFile("../../frontend/e2e/browser-security/headers.json")
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if err := json.Unmarshal(fixture, &headers); err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(headers["externalImage"], "{{origin}}", "https://images.example"); w.Header().Get("Content-Security-Policy") != want {
		t.Fatal("browser-test external image policy drift")
	}
	for name, want := range map[string]string{
		"Content-Type": "text/html; charset=utf-8", "X-Frame-Options": "SAMEORIGIN",
		"Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'; img-src https://images.example; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if strings.Contains(w.Body.String(), ` onerror="alert`) || strings.Contains(w.Body.String(), "<script") {
		t.Fatalf("unescaped URL in HTML: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "referrerpolicy=\"no-referrer\"") {
		t.Fatal("image referrer policy missing")
	}
}

func TestExternalImageRejectsUnsafeURLsAndRequiresAuth(t *testing.T) {
	mux := moduletest.Mount(&Module{})
	for _, raw := range []string{"", "//images.example/a", "javascript:alert(1)", "https://user:secret@example.com/x", "https://example.com;connect-src*/x", strings.Repeat("x", 8193)} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/previews/external-image?url="+url.QueryEscape(raw), nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("URL %q: status %d", raw, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/previews/external-image", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: %d", w.Code)
	}
	router := moduletest.Router{Mux: http.NewServeMux(), Auth: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	}}
	(&Module{}).Routes(router)
	w = httptest.NewRecorder()
	router.Mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/previews/external-image?url=https://example.com/a.png", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrapper bypassed auth: %d", w.Code)
	}
}
