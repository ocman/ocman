package sessioncatalog

import "database/sql"

// migratePluginOrigin admits the plugin origin, which like routine and factory
// names its source in origin_id. SQLite cannot alter a CHECK, so rebuild.
func migratePluginOrigin(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_catalog_v2 (
		id TEXT PRIMARY KEY CHECK (substr(id,1,4)='ocm_' AND length(id)=40),
		owner_id TEXT NOT NULL CHECK (length(trim(owner_id))>0),
		platform TEXT NOT NULL CHECK (length(trim(platform))>0),
		directory TEXT NOT NULL,title TEXT NOT NULL,
		parent_id TEXT REFERENCES session_catalog_v2(id),
		parent_native_id TEXT NOT NULL DEFAULT '',
		purged INTEGER NOT NULL DEFAULT 0 CHECK (purged IN (0,1)),
		origin TEXT NOT NULL CHECK (origin IN ('user','routine','factory','plugin','external')),
		origin_id TEXT NOT NULL DEFAULT '',
		creation_state TEXT NOT NULL CHECK (creation_state IN ('creating','configuring','ready','creation-failed')),
		creation_error TEXT NOT NULL DEFAULT '',
		request_key TEXT NOT NULL DEFAULT '',request_fingerprint TEXT NOT NULL DEFAULT '',
		source_key TEXT NOT NULL DEFAULT '',native_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,
		legacy_keys INTEGER NOT NULL DEFAULT 1 CHECK(legacy_keys IN (0,1)),
		CHECK ((origin IN ('routine','factory','plugin') AND length(trim(origin_id))>0)
			OR (origin IN ('user','external') AND origin_id='')),
		CHECK ((creation_state IN ('ready','configuring') AND source_key<>'' AND native_id<>'' AND creation_error='')
			OR (creation_state='creating' AND source_key='' AND native_id='' AND creation_error='')
			OR (creation_state='creation-failed' AND ((source_key='' AND native_id='') OR (source_key<>'' AND native_id<>'')) AND creation_error<>''))
	);
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
