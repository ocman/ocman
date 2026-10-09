package state

import (
	"context"
	"database/sql"
)

func migrateRoutineWorktrees(tx *sql.Tx) error {
	for _, table := range []string{"routine", "routine_run"} {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return err
		} else if !exists {
			continue
		}
		for _, column := range []string{"worktree", "cleanup_worktree"} {
			if err := addColumnIfMissing(tx, table, column, "INTEGER NOT NULL DEFAULT 0 CHECK ("+column+" IN (0, 1))"); err != nil {
				return err
			}
		}
		if table == "routine_run" {
			if err := addColumnIfMissing(tx, table, "worktree_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetRoutineRunWorktree records the owner-created workspace before prompting.
func (d *DB) SetRoutineRunWorktree(ctx context.Context, id, path string) error {
	result, err := d.db.ExecContext(ctx, `UPDATE routine_run SET worktree_path = ? WHERE id = ? AND state = 'running' AND worktree = 1`, path, id)
	if err != nil {
		return err
	}
	return requireRoutineChange(result)
}
