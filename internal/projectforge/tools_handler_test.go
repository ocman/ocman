package projectforge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPRToolHandlerAllowedRoutes(t *testing.T) {
	h := NewModule(Deps{}).PRToolHandler()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/project/upstreams"},
		{"GET", "/api/project/prs"},
		{"GET", "/api/project/pr-checks"},
		{"GET", "/api/project/pr-mergeability"},
		{"POST", "/api/project/handle"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/project/issues", "/api/project/forge-user", "/api/settings/prompt-templates", "/api/settings/source-control"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("unexpected route %s: %d", path, w.Code)
		}
	}
}
