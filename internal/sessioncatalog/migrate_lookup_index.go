package sessioncatalog

import "database/sql"

// Source-less legacy proofs must check all physical bindings, but not scan
// every session belonging to this owner for each reference being restored.
func migrateLegacyBindingIndex(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE INDEX session_catalog_legacy_binding ON session_catalog(owner_id,platform,native_id)`)
	return err
}
