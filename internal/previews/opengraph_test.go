package previews

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

func TestOpenGraphRequiresAppAccessAndRejectsLocalImages(t *testing.T) {
	s := newHarness(t)
	b := newBrowser()
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"http://127.0.0.1/secret","openGraph":true}`); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"previews":[]`) {
		t.Fatalf("unsafe fallback = %d %s", rr.Code, rr.Body.String())
	}
	if rr := b.do(t, s, http.MethodGet, "/api/previews/image?url=http://127.0.0.1/secret", ""); rr.Code != http.StatusBadGateway {
		t.Fatalf("unsafe image = %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/previews/image?url=https://example.com/image.png", nil)
	req.RemoteAddr, req.Host = "127.0.0.1:1", "localhost:8228"
	req.Header.Set("X-Forwarded-For", "192.0.2.4")
	rr := httptest.NewRecorder()
	s.mux().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("untrusted image = %d", rr.Code)
	}
}

func TestOpenGraphPrefersRichProviderCard(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"title":"Existing PR card","head":{"sha":"abc123"}}`)
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "github", Host: "github.com", APIBase: api.URL})
	s.Client = api.Client()
	rr := newBrowser().do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"https://github.com/o/r/pull/1","openGraph":true}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Existing PR card") || strings.Contains(rr.Body.String(), "opengraph") {
		t.Fatalf("provider precedence = %d %s", rr.Code, rr.Body.String())
	}
}
