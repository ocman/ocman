package sessioncatalog

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

// Earlier development builds recorded v2 without permitting configuring.
// Advancing the ledger through v6 must not hide that persisted constraint.
func legacyConfiguringSchema() state.ModuleSchema {
	legacy := Schema
	legacy.Tables = []string{"session_catalog", "session_remote_metadata", "session_source_cutover"}
	legacy.Migrations = []func(*sql.Tx) error{
		originalCatalogue,
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE session_catalog ADD COLUMN parent_native_id TEXT NOT NULL DEFAULT '';
				ALTER TABLE session_catalog ADD COLUMN purged INTEGER NOT NULL DEFAULT 0;`)
			return err
		},
		migrateLegacyKeys, migrateRemoteMetadata, migrateSourceCutover, migrateLegacyBindingIndex,
	}
	return legacy
}

func TestUpgradeAlreadyVersionedCatalogueAllowsDurableAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path, legacyConfiguringSchema())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	s, err = New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attach(t.Context(), row.ID, binding()); err != nil {
		_ = db.Close()
		t.Fatalf("upgraded v6 catalogue still loses native creation results: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s, err = New(db)
	if err != nil {
		t.Fatal(err)
	}
	replayed, created, err := s.Reserve(t.Context(), reservation())
	if err != nil || created || replayed.ID != row.ID || replayed.CreationState != Configuring || replayed.NativeID != binding().NativeID {
		t.Fatalf("restart lost creation receipt: %+v created=%v %v", replayed, created, err)
	}
	if _, err := s.Bind(t.Context(), row.ID, binding()); err != nil {
		t.Fatal(err)
	}
}
