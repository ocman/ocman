package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The favorites module is mounted from the registry against the server's
// state database and platform registry.
func TestFavoritesModuleMounted(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodPost, "/api/favorites", `{"platform":"opencode","provider":"anthropic","model":"claude-opus-4"}`, http.StatusNoContent},
		{http.MethodGet, "/api/favorites?platform=opencode", "", http.StatusOK},
		{http.MethodGet, "/api/favorites?platform=martian", "", http.StatusBadRequest},
		{http.MethodPut, "/api/favorites", "", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body)))
		if rec.Code != tt.want {
			t.Fatalf("%s %s = %d, want %d: %s", tt.method, tt.target, rec.Code, tt.want, rec.Body.String())
		}
		if tt.method == http.MethodGet && tt.want == http.StatusOK && !strings.Contains(rec.Body.String(), "claude-opus-4") {
			t.Fatalf("listed favorites = %s", rec.Body.String())
		}
	}
}
