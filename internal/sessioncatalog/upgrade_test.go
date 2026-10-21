package sessioncatalog

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

// This is the first catalogue layout that development builds could persist.
// Keep the fixture independent of the current migration implementation.
func originalCatalogue(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_catalog (
		id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,platform TEXT NOT NULL,
		directory TEXT NOT NULL CHECK(length(trim(directory))>0),title TEXT NOT NULL,
		parent_id TEXT REFERENCES session_catalog(id),
		origin TEXT NOT NULL,origin_id TEXT NOT NULL DEFAULT '',
		creation_state TEXT NOT NULL CHECK(creation_state IN ('creating','ready','creation-failed')),
		creation_error TEXT NOT NULL DEFAULT '',request_key TEXT NOT NULL DEFAULT '',request_fingerprint TEXT NOT NULL DEFAULT '',
		source_key TEXT NOT NULL DEFAULT '',native_id TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
		CHECK((creation_state='ready' AND source_key<>'' AND native_id<>'' AND creation_error='')
		 OR (creation_state='creating' AND source_key='' AND native_id='' AND creation_error='')
		 OR (creation_state='creation-failed' AND source_key='' AND native_id='' AND creation_error<>''))
	);
	CREATE UNIQUE INDEX session_catalog_request ON session_catalog(owner_id,request_key) WHERE request_key<>'';
	CREATE UNIQUE INDEX session_catalog_binding ON session_catalog(owner_id,platform,source_key,native_id) WHERE native_id<>'';`)
	return err
}

func TestUpgradeOriginalCataloguePreservesIdentitiesAndAddsMetadataColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	legacy := state.ModuleSchema{Module: Schema.Module, Tables: []string{"session_catalog"}, Migrations: []func(*sql.Tx) error{originalCatalogue}}
	db, err := state.Open(path, legacy)
	if err != nil {
		t.Fatal(err)
	}
	parent := "ocm_00000000-0000-4000-8000-000000000001"
	child := "ocm_00000000-0000-4000-8000-000000000002"
	_, err = db.SQL().ExecContext(t.Context(), `INSERT INTO session_catalog
		(id,owner_id,platform,directory,title,parent_id,origin,origin_id,creation_state,request_key,request_fingerprint,source_key,native_id,created_at,updated_at)
		VALUES (?, 'machine-1','opencode','/repo','parent',NULL,'routine','routine','ready','parent-request','parent-fingerprint','source-1','native-parent',1,2),
		(?, 'machine-1','opencode','/repo','child',?,'user','','ready','child-request','child-fingerprint','source-1','native-child',3,4)`, parent, child, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(t.Context(), "catalogue-upgrade", "keep"); err != nil {
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
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(t.Context(), child)
	if err != nil {
		t.Fatalf("original catalogue cannot be read after upgrade: %v", err)
	}
	if row.ParentID != parent || row.NativeID != "native-child" || row.Title != "child" || row.CreatedAt != 3 || row.UpdatedAt != 4 || !row.LegacyKeys {
		t.Fatalf("upgrade changed identity: %+v", row)
	}
	var request, fingerprint string
	if err := db.SQL().QueryRowContext(t.Context(), `SELECT request_key,request_fingerprint FROM session_catalog WHERE id=?`, child).Scan(&request, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if request != "child-request" || fingerprint != "child-fingerprint" {
		t.Fatalf("lost creation receipt: %q %q", request, fingerprint)
	}
	if value, ok, err := db.GetSetting(t.Context(), "catalogue-upgrade"); err != nil || !ok || value != "keep" {
		t.Fatalf("lost existing state: %q %v %v", value, ok, err)
	}
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if reserved.LegacyKeys {
		t.Fatal("new reservation inherited legacy deduplication identities")
	}
	if _, err := s.Attach(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatalf("upgraded constraint rejects configuring state: %v", err)
	}
	if err := s.Scrub(t.Context(), catalogSource(), "native-child"); err != nil {
		t.Fatalf("upgraded schema cannot scrub metadata: %v", err)
	}
}

func TestUpgradeDevelopmentParentColumnSpellings(t *testing.T) {
	for _, column := range []string{"parent_native_id", "native_parent_id"} {
		t.Run(column, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			legacy := state.ModuleSchema{Module: Schema.Module, Tables: []string{"session_catalog"}, Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
				if err := originalCatalogue(tx); err != nil {
					return err
				}
				_, err := tx.Exec(`ALTER TABLE session_catalog ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT '';
					ALTER TABLE session_catalog ADD COLUMN purged INTEGER NOT NULL DEFAULT 0;`)
				return err
			}}}
			db, err := state.Open(path, legacy)
			if err != nil {
				t.Fatal(err)
			}
			id := "ocm_00000000-0000-4000-8000-000000000003"
			_, err = db.SQL().ExecContext(t.Context(), `INSERT INTO session_catalog
				(id,owner_id,platform,directory,title,origin,creation_state,source_key,native_id,created_at,updated_at,`+column+`)
				VALUES (?,'machine-1','opencode','/repo','keep','external','ready','source-1','native-child',1,2,'native-parent')`, id)
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
			defer func() { _ = db.Close() }()
			s, err := New(db)
			if err != nil {
				t.Fatal(err)
			}
			row, err := s.Get(t.Context(), id)
			if err != nil || row.NativeParentID != "native-parent" || row.Title != "keep" || row.NativeID != "native-child" {
				t.Fatalf("development parent reference lost: %+v %v", row, err)
			}
			var version, violations int
			if err := db.SQL().QueryRowContext(t.Context(), `SELECT MAX(version) FROM module_schema_version WHERE module='sessioncatalog'`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err := db.SQL().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
				t.Fatal(err)
			}
			if version != len(Schema.Migrations) || violations != 0 {
				t.Fatalf("upgrade incomplete: v%d, %d FK violations", version, violations)
			}
		})
	}
}
