package statev2

import (
	"context"
	"fmt"
)

// Purge removes a conversation's queryable history and leaves an identity-only
// tombstone. Repeated purges are no-ops. Ingestion of that native identity stays
// suppressed forever, including after restart or a historical backfill.
// This is logical deletion, not a promise of erasure from backups or SQLite pages.
func (d *DB) Purge(ctx context.Context, key Key) error {
	if key.InstanceID == "" || key.SessionID == "" {
		return fmt.Errorf("statev2: purge requires a session identity")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO purged_session(instance_id,session_id) VALUES(?,?)
		ON CONFLICT DO NOTHING`, key.InstanceID, key.SessionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_outbox WHERE instance_id=? AND session_id=?`, key.InstanceID, key.SessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO change_outbox(instance_id,session_id,revision,kind) VALUES(?,?,0,'purged')`,
		key.InstanceID, key.SessionID); err != nil {
		return err
	}
	return tx.Commit()
}
