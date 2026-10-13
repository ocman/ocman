package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The analytics module is mounted behind the server's own guards: method
// first, then the localhost guard for subscription usage (it reads OpenCode's
// OAuth tokens) and auth for the rest.
func TestAnalyticsModuleMountedBehindServerGuards(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, peer string
		want               int
	}{
		{http.MethodGet, "/api/subscription-usage", "10.0.0.5:1234", http.StatusForbidden},
		{http.MethodPost, "/api/subscription-usage", "127.0.0.1:1234", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/analytics/overview", "127.0.0.1:1234", http.StatusOK},
		{http.MethodPost, "/api/analytics/overview", "127.0.0.1:1234", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.RemoteAddr = tc.peer
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s from %s = %d, want %d: %s", tc.method, tc.path, tc.peer, rec.Code, tc.want, rec.Body.String())
		}
	}
}
