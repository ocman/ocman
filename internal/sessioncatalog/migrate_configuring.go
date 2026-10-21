package sessioncatalog

import "database/sql"

// Some databases already marked metadata migration v2 applied before it gained
// the configuring constraint. A new ledger entry is required to upgrade them.
// Rebuild rather than rewriting sqlite_schema or editing a shipped migration.
func migrateConfiguringState(tx *sql.Tx) error {
	if err := createMetadataCatalogue(tx); err != nil {
		return err
	}
	_, err := tx.Exec(`ALTER TABLE session_catalog_v2 ADD COLUMN legacy_keys INTEGER NOT NULL DEFAULT 1 CHECK(legacy_keys IN (0,1));
		INSERT INTO session_catalog_v2 (
			id,owner_id,platform,directory,title,parent_id,parent_native_id,purged,origin,origin_id,
			creation_state,creation_error,request_key,request_fingerprint,source_key,native_id,created_at,updated_at,legacy_keys)
		SELECT id,owner_id,platform,directory,title,parent_id,parent_native_id,purged,origin,origin_id,
			creation_state,creation_error,request_key,request_fingerprint,source_key,native_id,created_at,updated_at,legacy_keys
		FROM session_catalog;
		DROP TABLE session_catalog;
		ALTER TABLE session_catalog_v2 RENAME TO session_catalog;
		CREATE UNIQUE INDEX session_catalog_request ON session_catalog(owner_id,request_key) WHERE request_key<>'';
		CREATE UNIQUE INDEX session_catalog_binding ON session_catalog(owner_id,platform,source_key,native_id) WHERE native_id<>'';
		CREATE INDEX session_catalog_list ON session_catalog(owner_id,created_at DESC,id);
		CREATE INDEX session_catalog_parent ON session_catalog(owner_id,platform,source_key,parent_native_id);
		CREATE INDEX session_catalog_creating ON session_catalog(owner_id,platform,directory) WHERE creation_state='creating';
		CREATE INDEX session_catalog_legacy_binding ON session_catalog(owner_id,platform,native_id);`)
	return err
}
