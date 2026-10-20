package sessioncatalog

import (
	"database/sql"

	"github.com/NoUseFreak/ocman/internal/state"
)

var Schema = state.ModuleSchema{
	Module:     "sessioncatalog",
	Tables:     []string{"session_catalog"},
	Migrations: []func(*sql.Tx) error{migrateIdentity, migrateCatalogueMetadata},
}

func migrateIdentity(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_catalog (
		id TEXT PRIMARY KEY CHECK (substr(id,1,4) = 'ocm_' AND length(id) = 40),
		owner_id TEXT NOT NULL CHECK (length(trim(owner_id)) > 0),
		platform TEXT NOT NULL CHECK (length(trim(platform)) > 0),
		directory TEXT NOT NULL CHECK (length(trim(directory)) > 0),
		title TEXT NOT NULL,
		parent_id TEXT REFERENCES session_catalog(id),
		origin TEXT NOT NULL CHECK (origin IN ('user','routine','factory','external')),
		origin_id TEXT NOT NULL DEFAULT '',
		creation_state TEXT NOT NULL CHECK (creation_state IN ('creating','ready','creation-failed')),
		creation_error TEXT NOT NULL DEFAULT '',
		request_key TEXT NOT NULL DEFAULT '',
		request_fingerprint TEXT NOT NULL DEFAULT '',
		source_key TEXT NOT NULL DEFAULT '',
		native_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		CHECK ((origin IN ('routine','factory') AND length(trim(origin_id)) > 0)
			OR (origin IN ('user','external') AND origin_id = '')),
		CHECK ((creation_state = 'ready' AND source_key <> '' AND native_id <> '' AND creation_error = '')
			OR (creation_state = 'creating' AND source_key = '' AND native_id = '' AND creation_error = '')
			OR (creation_state = 'creation-failed' AND source_key = '' AND native_id = '' AND creation_error <> ''))
	);
	CREATE UNIQUE INDEX session_catalog_request ON session_catalog(owner_id,request_key) WHERE request_key <> '';
	CREATE UNIQUE INDEX session_catalog_binding ON session_catalog(owner_id,platform,source_key,native_id) WHERE native_id <> '';
	CREATE INDEX session_catalog_list ON session_catalog(owner_id,created_at DESC,id);
	CREATE INDEX session_catalog_creating ON session_catalog(owner_id,platform,directory) WHERE creation_state = 'creating';`)
	return err
}
