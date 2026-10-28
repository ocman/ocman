package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestCanonicalReservedSessionRoutesAreNotSessionIdentities(t *testing.T) {
	s, _, _ := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	native, _ := s.registry.Get("test")
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Register(adapter)
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"archive", "seen", "pin"} {
		t.Run(path, func(t *testing.T) {
			called := false
			s.withOwnedSessionPath(func(http.ResponseWriter, *http.Request) { called = true })(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/session/"+path, nil))
			if !called {
				t.Fatal("reserved command resolved as a session identity")
			}
		})
	}
}
