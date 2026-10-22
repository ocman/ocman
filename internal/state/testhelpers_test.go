package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// Test-only fixtures. Production reaches these states through richer paths
// (claims, finishes, lifecycle transitions) that tests do not always need.

func (d *DB) UpdateRoutineRun(ctx context.Context, run RoutineRun) error {
	result, err := d.db.ExecContext(ctx, `UPDATE routine_run SET platform = ?, session_id = ?, state = ?, error = ?,
		started_at = CASE WHEN ? = 0 THEN started_at ELSE ? END,
		finished_at = CASE WHEN ? = 0 THEN finished_at ELSE ? END WHERE id = ?`,
		run.Platform, run.SessionID, run.State, run.Error, run.StartedAt, run.StartedAt, run.FinishedAt, run.FinishedAt, run.ID)
	if err != nil {
		return fmt.Errorf("updating routine run: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrRoutineRunNotFound
	}
	return nil
}

func (d *DB) IsSessionArchived(ctx context.Context, platform, sessionID string) (bool, error) {
	var one int
	err := d.db.QueryRowContext(ctx,
		`SELECT 1 FROM archived_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking archived session: %w", err)
	}
	return true, nil
}

func (d *DB) CountUnreadInboxItems(ctx context.Context) (int, error) {
	var count int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM inbox_item WHERE read_at IS NULL AND archived_at IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting unread Inbox items: %w", err)
	}
	return count, nil
}

// CreatePreparedFactoryAttempt allocates the next sequence for a Work Item
// without the claim's capacity and graph checks.
func (d *DB) CreatePreparedFactoryAttempt(ctx context.Context, epicID, workID string, policy model.FactoryAttemptPolicy, at time.Time) (model.FactoryAttempt, error) {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("encoding Factory attempt policy: %w", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("generating Factory attempt ID: %w", err)
	}
	id := "fa_" + hex.EncodeToString(random[:])
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("beginning Factory attempt creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var sequence int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM factory_attempt WHERE work_item_id = ?`, workID).Scan(&sequence); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("allocating Factory attempt sequence: %w", err)
	}
	now := at.UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_attempt
		(id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'prepared', ?, ?, ?)`, id, epicID, workID, sequence, string(policyJSON), now, now); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("creating prepared Factory attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("committing prepared Factory attempt: %w", err)
	}
	return model.FactoryAttempt{
		ID: id, EpicID: epicID, WorkID: workID, Sequence: sequence,
		Phase: model.FactoryAttemptPrepared, FrozenPolicy: policy,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// CompleteFactoryAttempt records success on an active attempt without the
// implementation profile's token and gate checks.
func (d *DB) CompleteFactoryAttempt(ctx context.Context, id string, result model.FactoryAttemptResult, at time.Time) (bool, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("encoding Factory attempt result: %w", err)
	}
	updated, err := d.db.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'terminal', terminal_outcome = 'succeeded', result_json = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND phase = 'active'
		AND NOT EXISTS (SELECT 1 FROM factory_project_request_gate WHERE attempt_id = ? AND resolution NOT IN ('approved', 'rejected'))`, string(resultJSON), at.UnixMilli(), at.UnixMilli(), id, id)
	return factoryAttemptChanged(updated, err, "completing Factory attempt")
}

func (d *DB) BeginSessionReplacementStop(ctx context.Context, platform, root string, baseline map[string]SessionInterruption, startedAt int64, handles ...ReplacementRuntime) error {
	snapshot := ReplacementStopSnapshot{Baseline: baseline, StartedAt: startedAt, AdmissionStartedAt: startedAt}
	if len(handles) > 0 {
		snapshot.Runtime = handles[0]
	}
	return d.BeginSessionReplacementStopSnapshot(ctx, platform, root, snapshot)
}
