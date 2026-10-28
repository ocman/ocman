package state

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ocman/ocman/internal/startup"
)

// One indexed transaction-local mapping replaces one full table/JSON scan per
// session with one update per reference column. Constraints still abort the
// entire transaction on collisions. Never disable uniqueness or merge rows.
func rekeyReferenceTables(ctx context.Context, tx *sql.Tx, platform string, keys []string, mapping map[string]string) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE session_rekey_map (
		old_id TEXT PRIMARY KEY, new_id TEXT NOT NULL UNIQUE,
		old_key TEXT NOT NULL UNIQUE, new_key TEXT NOT NULL UNIQUE
	) WITHOUT ROWID`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO session_rekey_map VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, old := range keys {
		if _, err := stmt.ExecContext(ctx, old, mapping[old], sessionKey(platform, old), sessionKey(platform, mapping[old])); err != nil {
			return err
		}
		startup.Report(ctx, "Preparing session reference mapping", i+1, len(keys))
	}
	total, completed := len(sessionReferenceColumns)+len(sessionJSONReferences)+3, 0
	report := func() {
		startup.Report(ctx, "Rekeying reference tables", completed, total)
		completed++
	}
	for _, ref := range sessionReferenceColumns {
		report()
		query := "UPDATE " + ref.table + " SET " + ref.column + "=(SELECT new_id FROM session_rekey_map WHERE old_id=" + ref.table + "." + ref.column + ") WHERE " + ref.owner + "=? AND " + ref.column + " IN (SELECT old_id FROM session_rekey_map)"
		if _, err := tx.ExecContext(ctx, query, platform); err != nil {
			return fmt.Errorf("rekeying %s.%s: %w", ref.table, ref.column, err)
		}
	}
	for _, column := range []string{"target_session_id", "child_session_id"} {
		report()
		if _, err := tx.ExecContext(ctx, "UPDATE loop_iterations SET "+column+"=(SELECT new_id FROM session_rekey_map WHERE old_id=loop_iterations."+column+") WHERE "+column+" IN (SELECT old_id FROM session_rekey_map) AND loop_id IN (SELECT id FROM loops WHERE platform=?)", platform); err != nil {
			return err
		}
	}
	for _, ref := range sessionJSONReferences {
		report()
		column := ref.table + "." + ref.column
		id := "json_extract(" + column + ",'$.sessionId')"
		query := "UPDATE " + ref.table + " SET " + ref.column + "=json_set(" + column + ",'$.sessionId',(SELECT new_id FROM session_rekey_map WHERE old_id=" + id + ")) WHERE " + ref.scope + " AND json_valid(" + column + ") AND COALESCE(json_extract(" + column + ",'$.platform'),'')=? AND " + id + " IN (SELECT old_id FROM session_rekey_map)"
		if _, err := tx.ExecContext(ctx, query, platform); err != nil {
			return err
		}
	}
	report()
	if _, err := tx.ExecContext(ctx, `UPDATE unarchived_entity
		SET entity_key=(SELECT new_key FROM session_rekey_map WHERE old_key=unarchived_entity.entity_key)
		WHERE kind='session' AND remote_id='local' AND entity_key IN (SELECT old_key FROM session_rekey_map)`); err != nil {
		return err
	}
	if platform == "opencode" {
		if _, err := tx.ExecContext(ctx, `UPDATE auto_approved_permission
			SET judge_session_id=(SELECT new_id FROM session_rekey_map WHERE old_id=auto_approved_permission.judge_session_id)
			WHERE judge_session_id IN (SELECT old_id FROM session_rekey_map)`); err != nil {
			return err
		}
	}
	startup.Report(ctx, "Rekeying reference tables", total, total)
	_, err = tx.ExecContext(ctx, `DROP TABLE session_rekey_map`)
	return err
}
