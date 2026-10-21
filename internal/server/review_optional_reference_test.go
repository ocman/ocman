package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

func TestCanonicalOptionalSessionReferencesPreserveAbsence(t *testing.T) {
	s, _, _ := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	native, _ := s.registry.Get("test")
	a, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Register(a)
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	scope := newSessionReferenceScope("test", "")
	for _, body := range []map[string]string{{"platform": "test"}, {"platform": "test", "parentSessionId": ""}} {
		if err := (&sessionViewWriter{server: s}).ProjectRequest(httptest.NewRequest(http.MethodPost, "/api/sessions", nil), &body); err != nil {
			t.Fatalf("empty optional parent body: %v", err)
		}
	}
	for _, parent := range []bool{false, true} {
		if id, err := scope.resolve(t.Context(), s, "", parent); err != nil || id != "" {
			t.Fatalf("optional reference became lookup: %q %v", id, err)
		}
	}
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/artifacts", "/api/plugins/actions?placement=global&surface=command-palette"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("unfiltered %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
