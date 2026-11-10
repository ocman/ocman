package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestConnectionFlagNormalizationPreservesRoutedRoutineAssociation(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := eventConnection(t, reg, "cnx_work", "local", "source")
	id := eventCatalogueRow(t, s, "local", "source")
	if err := s.stateDB.CreateRoutine(t.Context(), state.Routine{ID: "rt", Name: "Review", Prompt: "review", Directory: "/repo", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.stateDB.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "rt", Trigger: "manual", State: "running", OccurrenceAt: 1, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.LinkRoutineRun(t.Context(), "run", "cnx_work", id, 2, false); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.PinSession(t.Context(), "claudecode", id); err != nil {
		t.Fatal(err)
	}
	rows := []db.Session{{ID: id, Platform: "cnx_work", Status: db.StatusDone}}
	if err := s.applySessionStateReadOnly(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].RoutineID != "rt" || !rows[0].Pinned {
		t.Fatalf("flag identity erased routed workflow association: %+v", rows[0])
	}
	p.SessionFn = func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "claudecode", Directory: "/repo", Status: db.StatusDone}}, nil
	}
	r := httptest.NewRequest(http.MethodGet, "/api/session/"+id+"?platform=cnx_work&peek=1", nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.handleSession(w, r)
	var detail platforms.SessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK || detail.Session == nil || detail.Session.RoutineID != "rt" {
		t.Fatalf("detail lost routed association: %d %s %v", w.Code, w.Body.String(), err)
	}
}

func TestConnectionWorkflowTagsRetainMigratedAndExplicitAssociations(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	eventConnection(t, reg, "cnx_work", "local", "source")
	tags := map[state.Key]string{{Platform: "claudecode", SessionID: "migrated"}: "old-run", {Platform: "claudecode", SessionID: "explicit"}: "old-run", {Platform: "cnx_work", SessionID: "explicit"}: "named-run"}
	for id, want := range map[string]string{"migrated": "old-run", "explicit": "named-run", "missing": ""} {
		if got := s.sessionAssociationID(tags, "cnx_work", id); got != want {
			t.Fatalf("%s: got %s want %s", id, got, want)
		}
	}
}
