package sessioncatalog

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestAddingCataloguePreservesExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(t.Context(), "catalogue-upgrade-test", "retained"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	value, ok, err := db.GetSetting(t.Context(), "catalogue-upgrade-test")
	if err != nil || !ok || value != "retained" {
		t.Fatalf("state lost during catalogue migration: %q %v %v", value, ok, err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.Reserve(t.Context(), reservation()); err != nil || !created {
		t.Fatalf("catalogue unavailable after migration: %v %v", created, err)
	}
}

func TestSchemaRejectsIncompleteAndDuplicateBindings(t *testing.T) {
	s, db := openStore(t)
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE session_catalog SET creation_state='ready' WHERE id=?`,
		`UPDATE session_catalog SET creation_state='configuring' WHERE id=?`,
		`UPDATE session_catalog SET native_id='native' WHERE id=?`,
		`UPDATE session_catalog SET origin_id='' WHERE id=?`,
		`UPDATE session_catalog SET parent_id='missing-parent' WHERE id=?`,
		`UPDATE session_catalog SET creation_state='creation-failed' WHERE id=?`,
	} {
		if _, err := db.SQL().ExecContext(t.Context(), query, row.ID); err == nil {
			t.Errorf("accepted invalid catalogue state: %s", query)
		}
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); err != nil {
		t.Fatal(err)
	}
	r := reservation()
	r.RequestKey = "second"
	second, _, err := s.Reserve(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(t.Context(), `UPDATE session_catalog SET creation_state='ready', source_key=?,native_id=? WHERE id=?`, binding().SourceKey, binding().NativeID, second.ID); err == nil {
		t.Fatal("duplicate native binding accepted")
	}
}
