package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Keep runtime scope exactly as sampled, including an adopted handle's empty
// RepoRoot. The replacement's owner/root key is a separate identity.
type ReplacementRuntime struct {
	ManagedInstance
	RepoRoot string
}

// SessionReplacementStopping retains the exact cleanup handle across the crash
// between runtime shutdown and durable confirmation, independently of inventory.
func (d *DB) SessionReplacementStopping(ctx context.Context, platform, root string) (ReplacementRuntime, bool, error) {
	var data string
	err := d.db.QueryRowContext(ctx, `SELECT stop_runtime_json FROM session_replacement
		WHERE platform=? AND replacement_root=? AND phase='stopping'`, platform, root).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return ReplacementRuntime{}, false, nil
	}
	if err != nil {
		return ReplacementRuntime{}, false, err
	}
	if data == "" {
		return ReplacementRuntime{}, true, errors.New("unresolved stop has no persisted runtime handle")
	}
	var inst ReplacementRuntime
	if err := json.Unmarshal([]byte(data), &inst); err != nil {
		return ReplacementRuntime{}, true, err
	}
	if inst.Endpoint == "" {
		return ReplacementRuntime{}, true, errors.New("unresolved stop has an empty runtime endpoint")
	}
	return inst, true, nil
}

// CancelSessionReplacementStop is only for the host's positive validation that
// the original instance still runs. An inconclusive probe must not call it.
func (d *DB) CancelSessionReplacementStop(ctx context.Context, platform, root string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE session_replacement SET phase='prepared',
		stop_started_at=0, stop_baseline_json='{}', stop_runtime_json='',admission_started_at=0,reconciliation_initialized=0
		WHERE platform=? AND replacement_root=? AND phase='stopping'`, platform, root)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if err := clearReplacementWork(ctx, tx, platform, root); err != nil {
			return err
		}
	}
	return tx.Commit()
}
