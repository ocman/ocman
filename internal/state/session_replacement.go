package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrReplacementStopping = errors.New("unresolved stopping replacement must be reconciled before preparation")

func migrateToV116(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_replacement (
		platform TEXT NOT NULL, replacement_root TEXT NOT NULL,
		attempt INTEGER NOT NULL, phase TEXT NOT NULL,
		baseline_json TEXT NOT NULL,
		PRIMARY KEY (platform, replacement_root)
	);
	DELETE FROM session_interruption WHERE confirmed = 0`)
	// Old pending rows have neither settled baselines nor proof of a stopped
	// endpoint. They cannot safely be attributed to a replacement attempt.
	return err
}

// ReplacementStopped distinguishes confirmation recovery from a failed Stop.
// The host serializes replacement callbacks by owner-local root.
func (d *DB) ReplacementStopped(ctx context.Context, platform, root string) (bool, error) {
	var phase string
	err := d.db.QueryRowContext(ctx, `SELECT phase FROM session_replacement WHERE platform = ? AND replacement_root = ?`, platform, root).Scan(&phase)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return phase == "stopped", err
}

func (d *DB) MarkReplacementStopped(ctx context.Context, platform, root string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE session_replacement SET phase = 'stopped'
		WHERE platform = ? AND replacement_root = ? AND phase IN ('prepared', 'stopping')`, platform, root)
	return err
}

// PrepareSessionInterruptions replaces released-attempt evidence as one snapshot,
// including settled sessions. Stopping and stopped attempts retain their evidence.
func (d *DB) PrepareSessionInterruptions(ctx context.Context, platform, root string, baseline map[string]SessionInterruption, membership ...map[string]bool) error {
	data, err := json.Marshal(baseline)
	if err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO session_replacement
		(platform, replacement_root, attempt, phase, baseline_json) VALUES (?, ?, 1, 'prepared', ?)
		ON CONFLICT(platform, replacement_root) DO UPDATE SET
		attempt = session_replacement.attempt + 1, phase = 'prepared', baseline_json = excluded.baseline_json,
		stop_started_at = 0, stop_baseline_json = '{}', stop_runtime_json = '', admission_started_at=0,reconciliation_initialized=0
		WHERE session_replacement.phase NOT IN ('stopping', 'stopped')`, platform, root, string(data))
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
		if len(membership) > 0 {
			if err := saveReplacementMembership(ctx, tx, platform, root, membership[0]); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT phase FROM session_replacement WHERE platform=? AND replacement_root=?`, platform, root).Scan(&phase); err != nil {
		return err
	}
	if phase == "stopping" {
		return ErrReplacementStopping
	}
	return nil
}

// BeginSessionReplacementStop persists the refreshed evidence and Stop's entry
// boundary together. Only a host-validated live original can release an unresolved
// stopping attempt; crash recovery keeps the boundary, handle and baseline.
func (d *DB) BeginSessionReplacementStop(ctx context.Context, platform, root string, baseline map[string]SessionInterruption, startedAt int64, handles ...ReplacementRuntime) error {
	snapshot := ReplacementStopSnapshot{Baseline: baseline, StartedAt: startedAt, AdmissionStartedAt: startedAt}
	if len(handles) > 0 {
		snapshot.Runtime = handles[0]
	}
	return d.BeginSessionReplacementStopSnapshot(ctx, platform, root, snapshot)
}

func (d *DB) BeginSessionReplacementStopSnapshot(ctx context.Context, platform, root string, snapshot ReplacementStopSnapshot) error {
	data, err := json.Marshal(snapshot.Baseline)
	if err != nil {
		return err
	}
	runtimeJSON := ""
	if snapshot.Runtime.Endpoint != "" {
		data, err := json.Marshal(snapshot.Runtime)
		if err != nil {
			return err
		}
		runtimeJSON = string(data)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE session_replacement SET phase = 'stopping',
		stop_started_at = ?, stop_baseline_json = ?, stop_runtime_json = ?, admission_started_at=?,reconciliation_initialized=0
		WHERE platform = ? AND replacement_root = ? AND phase = 'prepared'`, snapshot.StartedAt, string(data), runtimeJSON, snapshot.AdmissionStartedAt, platform, root)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("replacement has no prepared stop attempt")
	}
	if snapshot.Membership != nil {
		if err := saveReplacementMembership(ctx, tx, platform, root, snapshot.Membership); err != nil {
			return err
		}
	}
	for i := range snapshot.Candidates {
		if prior, found := snapshot.Baseline[snapshot.Candidates[i].SessionID]; found {
			snapshot.Candidates[i].Prior = &prior
		}
	}
	if err := saveReplacementCandidates(ctx, tx, platform, root, snapshot.Candidates); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) SessionReplacementStopEvidence(ctx context.Context, platform, root string) (int64, map[string]SessionInterruption, error) {
	var startedAt int64
	var data string
	err := d.db.QueryRowContext(ctx, `SELECT stop_started_at, stop_baseline_json FROM session_replacement
		WHERE platform = ? AND replacement_root = ? AND phase IN ('stopping', 'stopped')`, platform, root).Scan(&startedAt, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	var baseline map[string]SessionInterruption
	err = json.Unmarshal([]byte(data), &baseline)
	return startedAt, baseline, err
}

func (d *DB) PreparedSessionInterruptions(ctx context.Context, platform, root string) (map[string]SessionInterruption, error) {
	var data string
	err := d.db.QueryRowContext(ctx, `SELECT baseline_json FROM session_replacement
		WHERE platform = ? AND replacement_root = ? AND phase IN ('prepared', 'stopping', 'stopped')`, platform, root).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var baseline map[string]SessionInterruption
	err = json.Unmarshal([]byte(data), &baseline)
	return baseline, err
}
