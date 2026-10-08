package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ManagedInstance is a persisted managed OpenCode instance. It mirrors
// the fields of ocruntime.Instance the host needs to re-probe a
// persisted row after a restart, but is a plain struct so internal/state
// stays decoupled from internal/ocruntime (the host layer converts).
type ManagedInstance struct {
	Endpoint   string
	Kind       string
	RuntimeID  string
	PID        int
	LaunchedAt time.Time
}

// ManagedOpencodes returns every persisted managed instance keyed by repo root.
func (d *DB) ManagedOpencodes(ctx context.Context) (map[string]ManagedInstance, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT repo_root, endpoint, kind, runtime_id, pid, launched_at FROM managed_opencode`)
	if err != nil {
		return nil, fmt.Errorf("listing managed opencode: %w", err)
	}
	defer rows.Close()
	out := make(map[string]ManagedInstance)
	for rows.Next() {
		var root string
		var inst ManagedInstance
		var launchedAt int64
		if err := rows.Scan(&root, &inst.Endpoint, &inst.Kind, &inst.RuntimeID, &inst.PID, &launchedAt); err != nil {
			return nil, fmt.Errorf("scanning managed opencode: %w", err)
		}
		inst.LaunchedAt = time.Unix(launchedAt, 0)
		out[root] = inst
	}
	return out, rows.Err()
}

// UpsertManagedOpencode records (or replaces) the managed instance for a
// project keyed by its canonical repo root. launchedAt is stored as a
// Unix-second timestamp.
func (d *DB) UpsertManagedOpencode(ctx context.Context, repoRoot string, inst ManagedInstance, launchedAt time.Time) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO managed_opencode (repo_root, endpoint, kind, runtime_id, pid, launched_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo_root) DO UPDATE SET
			endpoint    = excluded.endpoint,
			kind        = excluded.kind,
			runtime_id  = excluded.runtime_id,
			pid         = excluded.pid,
			launched_at = excluded.launched_at
	`, repoRoot, inst.Endpoint, inst.Kind, inst.RuntimeID, inst.PID, launchedAt.Unix())
	if err != nil {
		return fmt.Errorf("upserting managed opencode: %w", err)
	}
	return nil
}

// GetManagedOpencode returns the persisted managed instance for a repo
// root. ok is false when no row exists.
func (d *DB) GetManagedOpencode(ctx context.Context, repoRoot string) (ManagedInstance, bool, error) {
	var inst ManagedInstance
	var launchedAt int64
	err := d.db.QueryRowContext(ctx, `
		SELECT endpoint, kind, runtime_id, pid, launched_at
		FROM managed_opencode WHERE repo_root = ?
	`, repoRoot).Scan(&inst.Endpoint, &inst.Kind, &inst.RuntimeID, &inst.PID, &launchedAt)
	if err == sql.ErrNoRows {
		return ManagedInstance{}, false, nil
	}
	if err != nil {
		return ManagedInstance{}, false, fmt.Errorf("reading managed opencode: %w", err)
	}
	inst.LaunchedAt = time.Unix(launchedAt, 0)
	return inst, true, nil
}

// DeleteManagedOpencode removes the persisted row for a repo root.
// Idempotent: deleting an absent row is a no-op.
func (d *DB) DeleteManagedOpencode(ctx context.Context, repoRoot string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM managed_opencode WHERE repo_root = ?`, repoRoot)
	if err != nil {
		return fmt.Errorf("deleting managed opencode: %w", err)
	}
	return nil
}

// Confirmation runs under the owner's root singleflight. Retire its stopped
// inventory row before clearing the proof, in the same publication transaction.
// A saved handle must match; a newer/different row or another root is preserved.
// Legacy confirmation has no handle but still conveys closure of its pending
// root. A repeated confirmation of an already-confirmed attempt retires nothing.
func retireReplacementRuntime(ctx context.Context, tx *sql.Tx, platform, root string) error {
	if platform != "opencode" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM managed_opencode WHERE repo_root=? AND EXISTS(
		SELECT 1 FROM session_replacement r WHERE r.platform=? AND r.replacement_root=?
		AND r.phase IN ('prepared','stopping','stopped')
		AND (r.stop_runtime_json='' OR (
			managed_opencode.endpoint=COALESCE(json_extract(r.stop_runtime_json,'$.Endpoint'),'')
			AND managed_opencode.kind=COALESCE(json_extract(r.stop_runtime_json,'$.Kind'),'')
			AND managed_opencode.runtime_id=COALESCE(json_extract(r.stop_runtime_json,'$.RuntimeID'),'')
			AND managed_opencode.pid=COALESCE(json_extract(r.stop_runtime_json,'$.PID'),0))))`, root, platform, root)
	return err
}
