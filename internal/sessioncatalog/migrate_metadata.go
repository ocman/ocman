package sessioncatalog

import (
	"database/sql"
	"fmt"
)

// Development builds persisted several v1 layouts before metadata indexing
// existed. Inspect optional columns, then rebuild without changing identities,
// native bindings, parent links or idempotency receipts. Never reset the table.
func migrateCatalogueMetadata(tx *sql.Tx) error {
	columns, err := catalogueColumns(tx)
	if err != nil {
		return err
	}
	parent := "''"
	switch {
	case columns["parent_native_id"] && columns["native_parent_id"]:
		parent = "COALESCE(NULLIF(parent_native_id,''),native_parent_id,'')"
	case columns["parent_native_id"]:
		parent = "COALESCE(parent_native_id,'')"
	case columns["native_parent_id"]:
		parent = "COALESCE(native_parent_id,'')"
	}
	purged := "0"
	if columns["purged"] {
		purged = "purged"
	}
	if err := createMetadataCatalogue(tx); err != nil {
		return err
	}
	_, err = tx.Exec(fmt.Sprintf(`INSERT INTO session_catalog_v2 (
		id,owner_id,platform,directory,title,parent_id,parent_native_id,purged,origin,origin_id,
		creation_state,creation_error,request_key,request_fingerprint,source_key,native_id,created_at,updated_at)
		SELECT id,owner_id,platform,directory,title,parent_id,%s,%s,origin,origin_id,
		creation_state,creation_error,request_key,request_fingerprint,source_key,native_id,created_at,updated_at
		FROM session_catalog`, parent, purged))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE session_catalog;
		ALTER TABLE session_catalog_v2 RENAME TO session_catalog;
		CREATE UNIQUE INDEX session_catalog_request ON session_catalog(owner_id,request_key) WHERE request_key<>'';
		CREATE UNIQUE INDEX session_catalog_binding ON session_catalog(owner_id,platform,source_key,native_id) WHERE native_id<>'';
		CREATE INDEX session_catalog_list ON session_catalog(owner_id,created_at DESC,id);
		CREATE INDEX session_catalog_parent ON session_catalog(owner_id,platform,source_key,parent_native_id);
		CREATE INDEX session_catalog_creating ON session_catalog(owner_id,platform,directory) WHERE creation_state='creating';`)
	return err
}

func catalogueColumns(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(session_catalog)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func createMetadataCatalogue(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_catalog_v2 (
		id TEXT PRIMARY KEY CHECK (substr(id,1,4)='ocm_' AND length(id)=40),
		owner_id TEXT NOT NULL CHECK (length(trim(owner_id))>0),
		platform TEXT NOT NULL CHECK (length(trim(platform))>0),
		directory TEXT NOT NULL,title TEXT NOT NULL,
		parent_id TEXT REFERENCES session_catalog_v2(id),
		parent_native_id TEXT NOT NULL DEFAULT '',
		purged INTEGER NOT NULL DEFAULT 0 CHECK (purged IN (0,1)),
		origin TEXT NOT NULL CHECK (origin IN ('user','routine','factory','external')),
		origin_id TEXT NOT NULL DEFAULT '',
		creation_state TEXT NOT NULL CHECK (creation_state IN ('creating','configuring','ready','creation-failed')),
		creation_error TEXT NOT NULL DEFAULT '',
		request_key TEXT NOT NULL DEFAULT '',request_fingerprint TEXT NOT NULL DEFAULT '',
		source_key TEXT NOT NULL DEFAULT '',native_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
		CHECK ((origin IN ('routine','factory') AND length(trim(origin_id))>0)
			OR (origin IN ('user','external') AND origin_id='')),
		CHECK ((creation_state IN ('ready','configuring') AND source_key<>'' AND native_id<>'' AND creation_error='')
			OR (creation_state='creating' AND source_key='' AND native_id='' AND creation_error='')
			OR (creation_state='creation-failed' AND ((source_key='' AND native_id='') OR (source_key<>'' AND native_id<>'')) AND creation_error<>''))
	)`)
	return err
}
