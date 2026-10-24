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
		r := httptest.NewRequest(http.MethodGet, "/api/session/"+reference+"/queue", nil)
		r.SetPathValue("id", reference)
		s.withOwnedSessionPath(func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != id || r.URL.Query().Get("platform") != "test" {
				t.Fatalf("core received native reference: %s %s", r.PathValue("id"), r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("canonical path rejected: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestLegacyRequestReferenceBecomesCanonicalBeforeCoreMutation(t *testing.T) {
	s, id := canonicalHTTPFixture(t)
	info, err := s.sessions.LookupSessionReference(t.Context(), "test", "native")
	if err != nil || s.sessions.CoreSessionReference(info) != id {
		t.Fatalf("core mutation received native reference: %+v %v", info, err)
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

func TestCanonicalCapabilitiesEnableLegacyBrowserLinkAdoption(t *testing.T) {
	s, _ := canonicalHTTPFixture(t)
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var capabilities struct {
		OwnedSessionIDs bool `json:"ownedSessionIds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capabilities); err != nil || w.Code != http.StatusOK || !capabilities.OwnedSessionIDs {
		t.Fatalf("canonical browser-link adoption unavailable: %d %s %v", w.Code, w.Body.String(), err)
	}
}

func TestPendingSessionPathIsUnavailableBeforeReachingAnyHandler(t *testing.T) {
	s, _ := canonicalHTTPFixture(t)
	called := false
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/session/"+identity.PendingReference()+"/send?platform=test", nil)
	r.SetPathValue("id", identity.PendingReference())
	s.withOwnedSessionPath(func(http.ResponseWriter, *http.Request) { called = true })(w, r)
	if w.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("pending reference reached a session handler: %d %v", w.Code, called)
	}
}
