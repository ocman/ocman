package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModuleRouteOnlyResolvesDeclaredSessionPathParameters(t *testing.T) {
	for _, tc := range []struct {
		name     string
		register func(moduleRouter, string, http.HandlerFunc)
		local    bool
	}{
		{"Handle", moduleRouter.Handle, false},
		{"Local", moduleRouter.Local, true},
		{"Setting", moduleRouter.Setting, false},
		{"LocalSetting", moduleRouter.LocalSetting, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := canonicalHTTPFixture(t)
			if err := s.registry.EnableCanonicalSessions(); err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			router := moduleRouter{s: s, mux: mux}
			handler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			tc.register(router, "POST /api/session/static-command", handler)
			tc.register(router, "POST /api/session/{id}/command", handler)
			for _, request := range []struct {
				path, peer string
				status     int
			}{
				{"/api/session/static-command", "127.0.0.1:1234", http.StatusNoContent},
				{"/api/session/ocm_00000000-0000-0000-0000-000000000000/command", "127.0.0.1:1234", http.StatusNotFound},
			} {
				r := httptest.NewRequest(http.MethodPost, request.path, nil)
				r.RemoteAddr = request.peer
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != request.status {
					t.Fatalf("%s: got %d %s, want %d", request.path, w.Code, w.Body.String(), request.status)
				}
			}
			if tc.local {
				r := httptest.NewRequest(http.MethodPost, "/api/session/static-command", nil)
				r.RemoteAddr = "192.0.2.1:1234"
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("static route lost its localhost guard: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
