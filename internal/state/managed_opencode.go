package state

import (
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

// UpsertManagedOpencode records (or replaces) the managed instance for a
// project keyed by its canonical repo root. launchedAt is stored as a
// Unix-second timestamp.
func (d *DB) UpsertManagedOpencode(repoRoot string, inst ManagedInstance, launchedAt time.Time) error {
	_, err := d.db.Exec(`
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
func (d *DB) GetManagedOpencode(repoRoot string) (ManagedInstance, bool, error) {
	var inst ManagedInstance
	var launchedAt int64
	err := d.db.QueryRow(`
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
func (d *DB) DeleteManagedOpencode(repoRoot string) error {
	_, err := d.db.Exec(`DELETE FROM managed_opencode WHERE repo_root = ?`, repoRoot)
	if err != nil {
		return fmt.Errorf("deleting managed opencode: %w", err)
	}
	return nil
}
