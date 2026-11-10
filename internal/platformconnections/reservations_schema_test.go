package platformconnections

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestReservationSchemaUpgradePreservesConnections(t *testing.T) {
	previous := Schema
	previous.Tables = previous.Tables[:2]
	previous.Migrations = previous.Migrations[:1]
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(t.Context(), `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled,legacy_kind)
		VALUES('original-id','codex','Legacy account','existing','/legacy',0,'codex');
		INSERT INTO platform_connection_collection(id,onboarding_complete,default_id) VALUES(1,1,'original-id')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	c, err := s.Legacy(t.Context(), "codex")
	if err != nil || c.ID != "original-id" || c.Name != "Legacy account" || c.Enabled || c.Directory != "/legacy" {
		t.Fatalf("migration changed existing connection: %+v %v", c, err)
	}
	collection, err := s.Read(t.Context())
	if err != nil || collection.DefaultID != c.ID || !collection.OnboardingComplete {
		t.Fatalf("migration changed collection: %+v %v", collection, err)
	}
	r := reserve(t, s)
	if _, err := s.SaveReservation(t.Context(), r.ID, "New account", false); err != nil {
		t.Fatalf("reservation schema not available: %v", err)
	}
}
