package state

import (
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

func (d *DB) ListCurrentWorkflowVersions() ([]WorkflowVersion, error) {
	rows, err := d.db.Query(`
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		JOIN workflow_definition d ON d.id = v.workflow_id AND d.current_revision = v.revision
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

func (d *DB) ListQueuedWorkflowVersions() ([]WorkflowVersion, error) {
	rows, err := d.db.Query(`
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		JOIN workflow_trigger_firing f ON f.version_id = v.id AND f.decision = 'queued'
		JOIN workflow_definition d ON d.id = v.workflow_id
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

func (d *DB) GetWorkflowTriggerState(versionID, triggerID string) (WorkflowTriggerState, error) {
	var s WorkflowTriggerState
	var running sql.NullBool
	err := d.db.QueryRow(`SELECT version_id, trigger_id, detection_json,
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

func (d *DB) UpsertWorkflowTriggerState(s WorkflowTriggerState) error {
	return upsertWorkflowTriggerState(d.db, s)
}

func upsertWorkflowTriggerState(exec workflowRunExecer, s WorkflowTriggerState) error {
	var running interface{}
	if s.LastRunning != nil {
		running = *s.LastRunning
	}
	_, err := exec.Exec(`INSERT INTO workflow_trigger_state
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

func insertWorkflowTriggerFiring(exec workflowRunExecer, f WorkflowTriggerFiring) error {
	_, err := exec.Exec(`INSERT INTO workflow_trigger_firing
		(version_id, trigger_id, fired_at, detail, snapshot_json, decision, run_id, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, f.VersionID, f.TriggerID, f.FiredAt, f.Detail, f.SnapshotJSON, f.Decision, nullableString(f.RunID), nullableInt(f.StartedAt))
	if err != nil {
		return fmt.Errorf("inserting workflow trigger firing: %w", err)
	}
	return nil
}

func (d *DB) CommitWorkflowTriggerFiring(run *WorkflowRun, firing WorkflowTriggerFiring, triggerState WorkflowTriggerState) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow trigger firing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if run != nil {
		if err := insertWorkflowRun(tx, *run); err != nil {
			return err
		}
	}
	if err := insertWorkflowTriggerFiring(tx, firing); err != nil {
		return err
	}
	if err := upsertWorkflowTriggerState(tx, triggerState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow trigger firing: %w", err)
	}
	return nil
}

func (d *DB) CountActiveWorkflowTriggerRuns(versionID, triggerID string) (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT count(*) FROM workflow_run
		WHERE workflow_id = (SELECT workflow_id FROM workflow_version WHERE id = ?)
		AND state IN ('active', 'paused')
		AND json_extract(trigger_snapshot_json, '$.id') = ?`, versionID, triggerID).Scan(&count)
	return count, err
}

func (d *DB) ActiveWorkflowTriggerRunID(versionID, triggerID string) (string, error) {
	var id string
	err := d.db.QueryRow(`SELECT id FROM workflow_run
		WHERE workflow_id = (SELECT workflow_id FROM workflow_version WHERE id = ?)
		AND state IN ('active', 'paused')
		AND json_extract(trigger_snapshot_json, '$.id') = ?
		ORDER BY created_at, id LIMIT 1`, versionID, triggerID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (d *DB) CountQueuedWorkflowTriggerFirings(versionID, triggerID string) (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT count(*) FROM workflow_trigger_firing WHERE version_id = ? AND trigger_id = ? AND decision = 'queued'`, versionID, triggerID).Scan(&count)
	return count, err
}

func (d *DB) NextQueuedWorkflowTriggerFiring(versionID, triggerID string) (*WorkflowTriggerFiring, error) {
	var f WorkflowTriggerFiring
	err := d.db.QueryRow(`SELECT id, version_id, trigger_id, fired_at, detail, snapshot_json, decision,
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

func (d *DB) InsertWorkflowRunFromQueued(run WorkflowRun, firingID, startedAt int64, triggerState WorkflowTriggerState) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning queued workflow run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertWorkflowRun(tx, run); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE workflow_trigger_firing SET decision = 'started', run_id = ?, started_at = ? WHERE id = ? AND decision = 'queued'`, run.ID, startedAt, firingID)
	if err != nil {
		return fmt.Errorf("starting queued workflow firing: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("queued workflow firing is no longer available")
	}
	if err := upsertWorkflowTriggerState(tx, triggerState); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing queued workflow run: %w", err)
	}
	return nil
}
