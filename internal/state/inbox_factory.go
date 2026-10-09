package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SyncFactoryActionInbox persists outstanding decisions for one epic.
func (d *DB) SyncFactoryActionInbox(ctx context.Context, epicID, title string, actions map[string]string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	prefix := "factory-action-" + epicID + ":"
	now := time.Now().UnixMilli()
	live := make(map[string]bool, len(actions))
	for key, body := range actions {
		id := prefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
		live[id] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at, category) VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, id, title, body, now, InboxFactory); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM inbox_item WHERE substr(id, 1, ?) = ? AND archived_at IS NULL`, len(prefix), prefix)
	if err != nil {
		return err
	}
	var resolved []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if !live[id] {
			resolved = append(resolved, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range resolved {
		if _, err := tx.ExecContext(ctx, `UPDATE inbox_item SET archived_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
