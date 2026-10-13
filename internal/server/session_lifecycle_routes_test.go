package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleLaunchMethodsThroughFullMux(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/sessions/prepare", "/api/sessions/start"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405: %s", method, path, w.Code, w.Body.String())
			}
		}
	}
}
