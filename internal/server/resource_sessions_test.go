package server

import (
	"testing"

	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
)

// The local host identifies itself as "local", but the catalogue stores this
// machine's sessions under its instance owner. Resource attribution must
// translate it, or no local process links to a session or project.
func TestResourceSessionsUseCatalogueOwnerForLocalHost(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "instance", Platform: "claudecode", SourceKey: "source"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo", Title: "Local"}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{stateDB: core, historyOwner: "instance"}
	rows, err := s.resourceSessions(t.Context(), "local", 500, 0)
	if err != nil || len(rows) != 1 || rows[0].NativeID != "native" {
		t.Fatalf("local owner not translated: %+v %v", rows, err)
	}
	if rows, err := s.resourceSessions(t.Context(), "r-other", 500, 0); err != nil || len(rows) != 0 {
		t.Fatalf("remote owner read local sessions: %+v %v", rows, err)
	}
	if _, err := (&Server{stateDB: core}).resourceSessions(t.Context(), "local", 500, 0); err == nil {
		t.Fatal("missing instance owner must not fall back to another owner")
	}
}
