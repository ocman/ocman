package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

func canonicalHTTPFixture(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, id := identityHTTPFixture(t)
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
	return s, id
}

func TestCanonicalSessionPathsReachCoreWithoutNativeRewriting(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	for _, reference := range []string{id, "native"} {
		w := httptest.NewRecorder()
		s.withOwnedSessionPath(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/session/"+id+"/queue" || r.URL.Query().Get("platform") != "test" {
				t.Fatalf("core received native path: %s", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, httptest.NewRequest(http.MethodGet, "/api/session/"+reference+"/queue", nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("canonical path rejected: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestLegacyRequestReferenceBecomesCanonicalBeforeCoreMutation(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	scope := newSessionReferenceScope("test", "local")
	resolved, err := scope.resolve(t.Context(), s, "native", false)
	if err != nil || resolved != id {
		t.Fatalf("core mutation received native reference: %s %v", resolved, err)
	}
}

func TestCanonicalRoutineRequestsPersistOnlyCanonicalSessionReferences(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"name": "canonical", "prompt": "review", "directory": "/repo", "platform": "test", "remoteId": "local", "sessionMode": "existing", "sessionId": id, "schedule": map[string]string{"kind": "none"}})
	if err != nil {
		t.Fatal(err)
	}
	response := doRoutineRequest(t, mux, http.MethodPost, "/api/routines/", string(body))
	var item state.Routine
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || response.Code != http.StatusCreated {
		t.Fatalf("create routine: %d %s %v", response.Code, response.Body.String(), err)
	}
	stored, err := s.stateDB.GetRoutine(t.Context(), item.ID)
	if err != nil || stored.SessionID != id || stored.Platform != "test" {
		t.Fatalf("core persisted a native execution key: %+v %v", stored, err)
	}
}

func TestCanonicalIdentityTransportDoesNotExportNativeExecutionBinding(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	w := httptest.NewRecorder()
	s.handleSessionIdentity(w, httptest.NewRequest(http.MethodGet, "/api/session-identity?nativeId=native", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), "nativeId") || strings.Contains(w.Body.String(), "sourceKey") {
		t.Fatalf("canonical identity transport exposed binding data: %d %s", w.Code, w.Body.String())
	}
}

func TestPendingSessionPathIsUnavailableBeforeReachingAnyHandler(t *testing.T) {
	s, _ := canonicalHTTPFixture(t)
	called := false
	w := httptest.NewRecorder()
	s.withOwnedSessionPath(func(http.ResponseWriter, *http.Request) { called = true })(w, httptest.NewRequest(http.MethodPost, "/api/session/"+identity.PendingReference()+"/send?platform=test", nil))
	if w.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("pending reference reached a session handler: %d %v", w.Code, called)
	}
}
