package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestCanonicalSessionPathPreservesNamedConnectionRouting(t *testing.T) {
	for _, scope := range []string{"cnx_work", "", "cnx_missing"} {
		t.Run(scope, func(t *testing.T) {
			s, id := canonicalHTTPFixture(t)
			adapter, _ := s.registry.Get("test")
			s.registry.Unregister("test")
			if err := s.registry.RegisterConnection("cnx_work", adapter); err != nil {
				t.Fatal(err)
			}
			if err := s.registry.EnableCanonicalSessions(); err != nil {
				t.Fatal(err)
			}
			s.registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
				if owner == "owner" && kind == "test" && source == "source" {
					return "cnx_work", nil
				}
				return "", platforms.ErrNotFound
			})
			for _, reference := range []string{id, "native"} {
				r := httptest.NewRequest(http.MethodGet, "/api/session/"+reference+"?platform="+scope, nil)
				r.SetPathValue("id", reference)
				w := httptest.NewRecorder()
				called := false
				s.withOwnedSessionPath(func(w http.ResponseWriter, r *http.Request) {
					called = true
					if got := s.resolvePlatformForSession(w, r, r.PathValue("id")); got != nil {
						if got != adapter || r.PathValue("id") != id || r.URL.Query().Get("platform") != "cnx_work" {
							t.Fatal("lost canonical session or named account routing")
						}
						w.WriteHeader(http.StatusNoContent)
					}
				})(w, r)
				if scope == "cnx_missing" {
					if called || w.Code == http.StatusNoContent {
						t.Fatal("unknown explicit account fell back to another connection")
					}
					continue
				}
				if w.Code != http.StatusNoContent {
					t.Fatalf("sidebar session path rejected: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
