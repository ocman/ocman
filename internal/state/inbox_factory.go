package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// SyncFactoryActionInbox persists outstanding decisions for one epic and
// returns the bodies of the decisions that are new since the last sync.
func (d *DB) SyncFactoryActionInbox(ctx context.Context, epicID, title string, actions map[string]string) ([]string, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prefix := "factory-action-" + epicID + ":"
	now := time.Now().UnixMilli()
	live := make(map[string]bool, len(actions))
	var added []string
	for key, body := range actions {
		id := prefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
		live[id] = true
		result, err := tx.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at, category) VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, id, title, body, now, InboxFactory)
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			added = append(added, body)
		}
	}
	sort.Strings(added)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM inbox_item WHERE substr(id, 1, ?) = ? AND archived_at IS NULL`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	var resolved []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !live[id] {
			resolved = append(resolved, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range resolved {
		if _, err := tx.ExecContext(ctx, `UPDATE inbox_item SET archived_at = ? WHERE id = ?`, now, id); err != nil {
			return nil, err
		}
	}
	return added, tx.Commit()
}
