package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type WorkflowTriggerState struct {
	VersionID, TriggerID, DetectionJSON, LastDecision, LastRunID string
	LastCheckedAt, NextCheckAt, LastFiredAt                      int64
	LastRunning                                                  *bool
}

type WorkflowTriggerFiring struct {
	ID                                                          int64
	VersionID, TriggerID, Detail, SnapshotJSON, Decision, RunID string
	FiredAt, StartedAt                                          int64
}

// ListCurrentWorkflowVersions returns the versions the trigger
// scheduler may fire. That is the version the user activated, not the
// newest published revision: publishing does not activate (only revision
// 1 does), and deactivating is how a user stops a workflow running.
// Keying on current_revision instead fired revisions nobody activated
// and kept firing after a deactivation. Archiving clears
// active_version_id too, so the archived_at check is belt and braces.
func (d *DB) ListCurrentWorkflowVersions(ctx context.Context) ([]WorkflowVersion, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		JOIN workflow_definition d ON d.id = v.workflow_id AND d.active_version_id = v.id
		WHERE d.archived_at IS NULL
		ORDER BY v.created_at, v.id`)
	if err != nil {
		return nil, fmt.Errorf("listing current workflow versions: %w", err)
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		var v WorkflowVersion
		if err := rows.Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning current workflow version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListQueuedWorkflowVersions drains firings an overlap policy queued.
// A queued firing for a version that has since been deactivated is left
// in place rather than started: the user turned the workflow off after
// it queued, and reactivating releases the backlog.
func (d *DB) ListQueuedWorkflowVersions(ctx context.Context) ([]WorkflowVersion, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		JOIN workflow_trigger_firing f ON f.version_id = v.id AND f.decision = 'queued'
		JOIN workflow_definition d ON d.id = v.workflow_id AND d.active_version_id = v.id
		WHERE d.archived_at IS NULL
		GROUP BY v.id ORDER BY min(f.id)`)
	if err != nil {
		return nil, fmt.Errorf("listing queued workflow versions: %w", err)
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		var v WorkflowVersion
		if err := rows.Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning queued workflow version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) GetWorkflowTriggerState(ctx context.Context, versionID, triggerID string) (WorkflowTriggerState, error) {
	var s WorkflowTriggerState
	var running sql.NullBool
	err := d.db.QueryRowContext(ctx, `SELECT version_id, trigger_id, detection_json,
		COALESCE(last_checked_at, 0), COALESCE(next_check_at, 0), COALESCE(last_fired_at, 0),
		last_decision, COALESCE(last_run_id, ''), last_running
		FROM workflow_trigger_state WHERE version_id = ? AND trigger_id = ?`, versionID, triggerID).
		Scan(&s.VersionID, &s.TriggerID, &s.DetectionJSON, &s.LastCheckedAt, &s.NextCheckAt, &s.LastFiredAt, &s.LastDecision, &s.LastRunID, &running)
	if err != nil {
		return s, err
	}
	if running.Valid {
		value := running.Bool
		s.LastRunning = &value
	}
	return s, nil
}

func (d *DB) UpsertWorkflowTriggerState(ctx context.Context, s WorkflowTriggerState) error {
	return upsertWorkflowTriggerState(ctx, d.db, s)
}

func upsertWorkflowTriggerState(ctx context.Context, exec workflowRunExecer, s WorkflowTriggerState) error {
	var running interface{}
	if s.LastRunning != nil {
		running = *s.LastRunning
	}
	_, err := exec.ExecContext(ctx, `INSERT INTO workflow_trigger_state
		(version_id, trigger_id, detection_json, last_checked_at, next_check_at, last_fired_at, last_decision, last_run_id, last_running)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(version_id, trigger_id) DO UPDATE SET detection_json=excluded.detection_json,
		last_checked_at=excluded.last_checked_at, next_check_at=excluded.next_check_at,
		last_fired_at=excluded.last_fired_at, last_decision=excluded.last_decision,
		last_run_id=excluded.last_run_id, last_running=excluded.last_running`,
		s.VersionID, s.TriggerID, s.DetectionJSON, nullableInt(s.LastCheckedAt), nullableInt(s.NextCheckAt), nullableInt(s.LastFiredAt), s.LastDecision, nullableString(s.LastRunID), running)
	if err != nil {
		return fmt.Errorf("saving workflow trigger state: %w", err)
	}
	return nil
}

func insertWorkflowTriggerFiring(ctx context.Context, exec workflowRunExecer, f WorkflowTriggerFiring) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO workflow_trigger_firing
		(version_id, trigger_id, fired_at, detail, snapshot_json, decision, run_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, f.VersionID, f.TriggerID, f.FiredAt, f.Detail, f.SnapshotJSON, f.Decision, nullableString(f.RunID), nullableInt(f.StartedAt))
	if err != nil {
		return fmt.Errorf("inserting workflow trigger firing: %w", err)
	}
	return nil
}

func (d *DB) CommitWorkflowTriggerFiring(ctx context.Context, run *WorkflowRun, firing WorkflowTriggerFiring, triggerState WorkflowTriggerState) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning workflow trigger firing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if run != nil {
		if err := insertWorkflowRun(ctx, tx, *run); err != nil {
			return err
		}
	}
	if err := insertWorkflowTriggerFiring(ctx, tx, firing); err != nil {
		return err
	}
	if err := upsertWorkflowTriggerState(ctx, tx, triggerState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow trigger firing: %w", err)
	}
	return nil
}

func (d *DB) CountActiveWorkflowTriggerRuns(ctx context.Context, versionID, triggerID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM workflow_run
		WHERE workflow_id = (SELECT workflow_id FROM workflow_version WHERE id = ?)
		AND state IN ('active', 'paused')
		AND json_extract(trigger_snapshot_json, '$.id') = ?`, versionID, triggerID).Scan(&count)
	return count, err
}

func (d *DB) ActiveWorkflowTriggerRunID(ctx context.Context, versionID, triggerID string) (string, error) {
	var id string
	err := d.db.QueryRowContext(ctx, `SELECT id FROM workflow_run
		WHERE workflow_id = (SELECT workflow_id FROM workflow_version WHERE id = ?)
		AND state IN ('active', 'paused')
		AND json_extract(trigger_snapshot_json, '$.id') = ?
		ORDER BY created_at, id LIMIT 1`, versionID, triggerID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (d *DB) CountQueuedWorkflowTriggerFirings(ctx context.Context, versionID, triggerID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM workflow_trigger_firing WHERE version_id = ? AND trigger_id = ? AND decision = 'queued'`, versionID, triggerID).Scan(&count)
	return count, err
}

func (d *DB) NextQueuedWorkflowTriggerFiring(ctx context.Context, versionID, triggerID string) (*WorkflowTriggerFiring, error) {
	var f WorkflowTriggerFiring
	err := d.db.QueryRowContext(ctx, `SELECT id, version_id, trigger_id, fired_at, detail, snapshot_json, decision,
		COALESCE(run_id, ''), COALESCE(started_at, 0) FROM workflow_trigger_firing
		WHERE version_id = ? AND trigger_id = ? AND decision = 'queued' ORDER BY id LIMIT 1`, versionID, triggerID).
		Scan(&f.ID, &f.VersionID, &f.TriggerID, &f.FiredAt, &f.Detail, &f.SnapshotJSON, &f.Decision, &f.RunID, &f.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting queued workflow firing: %w", err)
	}
	return &f, nil
}

func (d *DB) InsertWorkflowRunFromQueued(ctx context.Context, run WorkflowRun, firingID, startedAt int64, triggerState WorkflowTriggerState) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning queued workflow run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertWorkflowRun(ctx, tx, run); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE workflow_trigger_firing SET decision = 'started', run_id = ?, started_at = ? WHERE id = ? AND decision = 'queued'`, run.ID, startedAt, firingID)
	if err != nil {
		return fmt.Errorf("starting queued workflow firing: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("queued workflow firing is no longer available")
	}
	if err := upsertWorkflowTriggerState(ctx, tx, triggerState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing queued workflow run: %w", err)
	}
	return nil
}
