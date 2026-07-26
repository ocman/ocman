package state

import (
	"database/sql"
	"errors"
	"fmt"
)

// The run mirror projects an external runner's view of a run back onto
// ocman's own tables, so history, the API, and the UI keep working
// unchanged while something else does the executing. It is strictly
// one-way: ocman never reads execution state back out of these rows to
// drive scheduling.

// WorkflowMirrorNode is one node's externally observed outcome.
type WorkflowMirrorNode struct {
	NodeID      string
	State       string
	StartedAt   int64
	CompletedAt int64
	Stdout      string
	Stderr      string
	ExitCode    int
	Error       string
}

// WorkflowMirrorSnapshot is a whole run as the external runner sees it.
type WorkflowMirrorSnapshot struct {
	State       string
	CompletedAt int64
	Nodes       []WorkflowMirrorNode
}

// ExternalWorkflowRun identifies a run driven by an external runner.
type ExternalWorkflowRun struct {
	RunID      string
	WorkflowID string
	ExternalID string
	Runner     string
}

// SetWorkflowRunExternal records which external execution drives a run.
func (d *DB) SetWorkflowRunExternal(runID, externalID, runner string, now int64) error {
	if _, err := d.db.Exec(
		`UPDATE workflow_run SET external_run_id = ?, external_runner = ?, updated_at = ? WHERE id = ?`,
		externalID, runner, now, runID); err != nil {
		return fmt.Errorf("linking workflow run to external runner: %w", err)
	}
	return nil
}

// ListActiveExternalWorkflowRuns returns the runs the mirror still has
// to poll. Terminal runs are excluded: their rows are final, and a
// restart must not resurrect them.
func (d *DB) ListActiveExternalWorkflowRuns() ([]ExternalWorkflowRun, error) {
	rows, err := d.db.Query(
		`SELECT id, workflow_id, external_run_id, external_runner FROM workflow_run
		 WHERE external_run_id != '' AND state IN ('active', 'paused') ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("listing external workflow runs: %w", err)
	}
	defer rows.Close()
	var out []ExternalWorkflowRun
	for rows.Next() {
		var run ExternalWorkflowRun
		if err := rows.Scan(&run.RunID, &run.WorkflowID, &run.ExternalID, &run.Runner); err != nil {
			return nil, fmt.Errorf("scanning external workflow run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// MirrorWorkflowRun writes a snapshot onto the run's rows and reports
// whether anything actually changed. Polling is frequent and mostly
// idle, so an unchanged snapshot must not touch the database or wake
// every SSE subscriber.
func (d *DB) MirrorWorkflowRun(runID string, snapshot WorkflowMirrorSnapshot, now int64) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning workflow mirror: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	changed := false
	var current string
	switch err := tx.QueryRow(`SELECT state FROM workflow_run WHERE id = ?`, runID).Scan(&current); {
	case errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("mirroring unknown workflow run %q", runID)
	case err != nil:
		return false, fmt.Errorf("reading workflow run state: %w", err)
	}
	if snapshot.State != "" && snapshot.State != current {
		if _, err := tx.Exec(
			`UPDATE workflow_run SET state = ?, updated_at = ?, completed_at = ? WHERE id = ?`,
			snapshot.State, now, nullableInt(snapshot.CompletedAt), runID); err != nil {
			return false, fmt.Errorf("mirroring workflow run state: %w", err)
		}
		changed = true
	}
	for _, node := range snapshot.Nodes {
		nodeChanged, err := mirrorWorkflowNode(tx, runID, node)
		if err != nil {
			return false, err
		}
		changed = changed || nodeChanged
	}
	if !changed {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing workflow mirror: %w", err)
	}
	return true, nil
}

func mirrorWorkflowNode(tx *sql.Tx, runID string, node WorkflowMirrorNode) (bool, error) {
	var currentState string
	switch err := tx.QueryRow(
		`SELECT state FROM workflow_node_run WHERE run_id = ? AND node_id = ?`,
		runID, node.NodeID).Scan(&currentState); {
	case errors.Is(err, sql.ErrNoRows):
		// The runner reported a step ocman does not model, such as a
		// mapped child's internal step. Nothing to mirror onto.
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading workflow node state: %w", err)
	}

	var attemptState, stdout, stderr, attemptError string
	var exitCode int
	existing := tx.QueryRow(
		`SELECT state, COALESCE(stdout, ''), COALESCE(stderr, ''), COALESCE(exit_code, 0), COALESCE(error, '')
		 FROM workflow_node_attempt WHERE run_id = ? AND node_id = ? AND seq = 1`, runID, node.NodeID)
	hasAttempt := true
	if err := existing.Scan(&attemptState, &stdout, &stderr, &exitCode, &attemptError); errors.Is(err, sql.ErrNoRows) {
		hasAttempt = false
	} else if err != nil {
		return false, fmt.Errorf("reading workflow attempt: %w", err)
	}

	unchanged := currentState == node.State && hasAttempt &&
		attemptState == node.State && stdout == node.Stdout &&
		stderr == node.Stderr && exitCode == node.ExitCode && attemptError == node.Error
	if unchanged {
		return false, nil
	}
	if _, err := tx.Exec(
		`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ?`,
		node.State, nullableInt(node.CompletedAt), runID, node.NodeID); err != nil {
		return false, fmt.Errorf("mirroring workflow node state: %w", err)
	}
	// ponytail: one attempt row per node, updated in place. Dagu's own
	// per-step retries collapse into it; give each retryCount its own seq
	// if per-attempt history for command steps is ever needed.
	if _, err := tx.Exec(
		`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at, completed_at, stdout, stderr, exit_code, error)
		 VALUES (?, ?, 1, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (run_id, node_id, seq) DO UPDATE SET
		   state = excluded.state, completed_at = excluded.completed_at,
		   stdout = excluded.stdout, stderr = excluded.stderr,
		   exit_code = excluded.exit_code, error = excluded.error`,
		runID, node.NodeID, node.State, node.StartedAt, nullableInt(node.CompletedAt),
		node.Stdout, node.Stderr, node.ExitCode, node.Error); err != nil {
		return false, fmt.Errorf("mirroring workflow attempt: %w", err)
	}
	return true, nil
}

// GetWorkflowRunExternal returns the external execution driving a run,
// or an empty record when ocman's own dispatcher owns it.
func (d *DB) GetWorkflowRunExternal(runID string) (ExternalWorkflowRun, error) {
	run := ExternalWorkflowRun{RunID: runID}
	err := d.db.QueryRow(
		`SELECT workflow_id, external_run_id, external_runner FROM workflow_run WHERE id = ?`,
		runID).Scan(&run.WorkflowID, &run.ExternalID, &run.Runner)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalWorkflowRun{}, fmt.Errorf("unknown workflow run %q", runID)
	}
	if err != nil {
		return ExternalWorkflowRun{}, fmt.Errorf("reading external workflow run: %w", err)
	}
	return run, nil
}
