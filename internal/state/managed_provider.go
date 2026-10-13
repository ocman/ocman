package state

import (
	"context"
	"database/sql"
	"time"
)

func migrateToV113(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS managed_provider (
		provider TEXT NOT NULL, repo_root TEXT NOT NULL,
		endpoint TEXT NOT NULL, kind TEXT NOT NULL, runtime_id TEXT NOT NULL,
		pid INTEGER NOT NULL, launched_at INTEGER NOT NULL,
		PRIMARY KEY (provider, repo_root))`)
	return err
}

func (d *DB) GetManagedProvider(ctx context.Context, provider, root string) (ManagedInstance, bool, error) {
	var inst ManagedInstance
	var at int64
	err := d.db.QueryRowContext(ctx, `SELECT endpoint, kind, runtime_id, pid, launched_at FROM managed_provider WHERE provider=? AND repo_root=?`, provider, root).
		Scan(&inst.Endpoint, &inst.Kind, &inst.RuntimeID, &inst.PID, &at)
	if err == sql.ErrNoRows {
		return inst, false, nil
	}
	inst.LaunchedAt = time.Unix(at, 0)
	return inst, err == nil, err
}

func (d *DB) UpsertManagedProvider(ctx context.Context, provider, root string, inst ManagedInstance) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO managed_provider (provider, repo_root, endpoint, kind, runtime_id, pid, launched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(provider, repo_root) DO UPDATE SET
		endpoint=excluded.endpoint, kind=excluded.kind, runtime_id=excluded.runtime_id, pid=excluded.pid, launched_at=excluded.launched_at`,
		provider, root, inst.Endpoint, inst.Kind, inst.RuntimeID, inst.PID, inst.LaunchedAt.Unix())
	return err
}

func (d *DB) DeleteManagedProvider(ctx context.Context, provider, root string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM managed_provider WHERE provider=? AND repo_root=?`, provider, root)
	return err
}

// ListManagedProviders returns the repo roots with a persisted supervisor.
func (d *DB) ListManagedProviders(ctx context.Context, provider string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT repo_root FROM managed_provider WHERE provider=? ORDER BY repo_root`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}
