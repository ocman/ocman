package state

import (
	"database/sql"
	"errors"
	"fmt"
)

type WorkflowVersion struct {
	ID              string
	WorkflowID      string
	Name            string
	Revision        int
	MetadataVersion string
	DefinitionJSON  string
	Concurrency     int
	CreatedAt       int64
	Nodes           []WorkflowNode
	Dependencies    []WorkflowDependency
}

type WorkflowNode struct {
	ID       string
	Name     string
	Type     string
	Position int
}

type WorkflowDependency struct {
	From string
	To   string
}

type WorkflowRun struct {
	ID                  string
	WorkflowID          string
	VersionID           string
	State               string
	CreatedAt           int64
	UpdatedAt           int64
	CompletedAt         int64
	TriggerSnapshotJSON string
	Nodes               []WorkflowNodeRun
}

type WorkflowNodeRun struct {
	NodeID      string
	Name        string
	Type        string
	State       string
	Position    int
	ReadyAt     int64
	CompletedAt int64
	Attempts    []WorkflowAttempt
}

type WorkflowAttempt struct {
	ID              int64
	Seq             int
	State           string
	StartedAt       int64
	CompletedAt     int64
	ExitCode        *int
	Stdout          string
	Stderr          string
	Error           string
	OutputsJSON     string
	StdoutTruncated bool
	StderrTruncated bool
	Platform        string
	SessionID       string
	SessionState    string
	Affinity        string
	Directory       string
}

type WorkflowCommandResult struct {
	State           string
	ExitCode        int
	Stdout          string
	Stderr          string
	Error           string
	OutputsJSON     string
	StdoutTruncated bool
	StderrTruncated bool
}

func (d *DB) InsertWorkflowVersion(v WorkflowVersion) (WorkflowVersion, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return WorkflowVersion{}, fmt.Errorf("beginning workflow publish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var revision int
	err = tx.QueryRow(`SELECT current_revision FROM workflow_definition WHERE id = ?`, v.WorkflowID).Scan(&revision)
	switch {
	case err == nil:
		revision++
		if _, err = tx.Exec(`UPDATE workflow_definition SET name = ?, current_revision = ?, updated_at = ? WHERE id = ?`, v.Name, revision, v.CreatedAt, v.WorkflowID); err != nil {
			return WorkflowVersion{}, fmt.Errorf("updating workflow definition: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		revision = 1
		if _, err = tx.Exec(`INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, v.WorkflowID, v.Name, revision, v.CreatedAt, v.CreatedAt); err != nil {
			return WorkflowVersion{}, fmt.Errorf("inserting workflow definition: %w", err)
		}
	default:
		return WorkflowVersion{}, fmt.Errorf("reading workflow revision: %w", err)
	}
	v.Revision = revision
	if _, err = tx.Exec(`INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, v.ID, v.WorkflowID, v.Name, v.Revision, v.MetadataVersion, v.DefinitionJSON, v.Concurrency, v.CreatedAt); err != nil {
		return WorkflowVersion{}, fmt.Errorf("inserting workflow version: %w", err)
	}
	for _, node := range v.Nodes {
		if _, err = tx.Exec(`INSERT INTO workflow_version_node (version_id, node_id, name, type, position) VALUES (?, ?, ?, ?, ?)`, v.ID, node.ID, node.Name, node.Type, node.Position); err != nil {
			return WorkflowVersion{}, fmt.Errorf("inserting workflow node: %w", err)
		}
	}
	for _, dep := range v.Dependencies {
		if _, err = tx.Exec(`INSERT INTO workflow_version_dependency (version_id, from_node, to_node) VALUES (?, ?, ?)`, v.ID, dep.From, dep.To); err != nil {
			return WorkflowVersion{}, fmt.Errorf("inserting workflow dependency: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return WorkflowVersion{}, fmt.Errorf("committing workflow publish: %w", err)
	}
	return v, nil
}

func (d *DB) GetWorkflowVersion(id string) (*WorkflowVersion, error) {
	var v WorkflowVersion
	err := d.db.QueryRow(`
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		WHERE v.id = ?`, id).Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting workflow version: %w", err)
	}
	v.Nodes, err = d.workflowVersionNodes(id)
	if err != nil {
		return nil, err
	}
	v.Dependencies, err = d.workflowVersionDependencies(id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (d *DB) GetActiveWorkflowVersion(workflowID string) (*WorkflowVersion, error) {
	var id string
	err := d.db.QueryRow(`
		SELECT v.id FROM workflow_version v
		JOIN workflow_definition d ON d.id = v.workflow_id AND d.current_revision = v.revision
		WHERE d.id = ?`, workflowID).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("getting active workflow version: %w", err)
	}
	return d.GetWorkflowVersion(id)
}

func (d *DB) ListWorkflowVersions() ([]WorkflowVersion, error) {
	rows, err := d.db.Query(`
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.created_at
		FROM workflow_version v
		ORDER BY v.created_at DESC, v.revision DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing workflow versions: %w", err)
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		var v WorkflowVersion
		if err := rows.Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning workflow version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) workflowVersionNodes(id string) ([]WorkflowNode, error) {
	rows, err := d.db.Query(`SELECT node_id, name, type, position FROM workflow_version_node WHERE version_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("listing workflow nodes: %w", err)
	}
	defer rows.Close()
	var out []WorkflowNode
	for rows.Next() {
		var node WorkflowNode
		if err := rows.Scan(&node.ID, &node.Name, &node.Type, &node.Position); err != nil {
			return nil, fmt.Errorf("scanning workflow node: %w", err)
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (d *DB) workflowVersionDependencies(id string) ([]WorkflowDependency, error) {
	rows, err := d.db.Query(`SELECT from_node, to_node FROM workflow_version_dependency WHERE version_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, fmt.Errorf("listing workflow dependencies: %w", err)
	}
	defer rows.Close()
	var out []WorkflowDependency
	for rows.Next() {
		var dep WorkflowDependency
		if err := rows.Scan(&dep.From, &dep.To); err != nil {
			return nil, fmt.Errorf("scanning workflow dependency: %w", err)
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

func (d *DB) InsertWorkflowRun(run WorkflowRun) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertWorkflowRun(tx, run); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow run: %w", err)
	}
	return nil
}

type workflowRunExecer interface {
	Exec(string, ...interface{}) (sql.Result, error)
}

func insertWorkflowRun(exec workflowRunExecer, run WorkflowRun) error {
	if _, err := exec.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at, trigger_snapshot_json) VALUES (?, ?, ?, ?, ?, ?, ?)`, run.ID, run.WorkflowID, run.VersionID, run.State, run.CreatedAt, run.UpdatedAt, nullableString(run.TriggerSnapshotJSON)); err != nil {
		return fmt.Errorf("inserting workflow run: %w", err)
	}
	for _, node := range run.Nodes {
		if _, err := exec.Exec(`INSERT INTO workflow_node_run (run_id, node_id, state, position, ready_at) VALUES (?, ?, ?, ?, ?)`, run.ID, node.NodeID, node.State, node.Position, nullableInt(node.ReadyAt)); err != nil {
			return fmt.Errorf("inserting workflow node run: %w", err)
		}
		if node.State == "ready" {
			if _, err := exec.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, run.ID, node.NodeID, run.CreatedAt); err != nil {
				return fmt.Errorf("inserting approval attempt: %w", err)
			}
		}
	}
	return nil
}

func (d *DB) ListWorkflowRuns() ([]WorkflowRun, error) {
	rows, err := d.db.Query(`SELECT id, workflow_id, version_id, state, created_at, updated_at, COALESCE(completed_at, 0), COALESCE(trigger_snapshot_json, '') FROM workflow_run ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing workflow runs: %w", err)
	}
	defer rows.Close()
	var out []WorkflowRun
	for rows.Next() {
		var run WorkflowRun
		if err := rows.Scan(&run.ID, &run.WorkflowID, &run.VersionID, &run.State, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt, &run.TriggerSnapshotJSON); err != nil {
			return nil, fmt.Errorf("scanning workflow run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (d *DB) GetWorkflowRun(id string) (*WorkflowRun, error) {
	var run WorkflowRun
	err := d.db.QueryRow(`SELECT id, workflow_id, version_id, state, created_at, updated_at, COALESCE(completed_at, 0), COALESCE(trigger_snapshot_json, '') FROM workflow_run WHERE id = ?`, id).Scan(&run.ID, &run.WorkflowID, &run.VersionID, &run.State, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt, &run.TriggerSnapshotJSON)
	if err != nil {
		return nil, fmt.Errorf("getting workflow run: %w", err)
	}
	rows, err := d.db.Query(`
		SELECT nr.node_id, n.name, n.type, nr.state, nr.position,
		       COALESCE(nr.ready_at, 0), COALESCE(nr.completed_at, 0)
		FROM workflow_node_run nr
		JOIN workflow_version_node n ON n.version_id = ? AND n.node_id = nr.node_id
		WHERE nr.run_id = ? ORDER BY nr.position`, run.VersionID, id)
	if err != nil {
		return nil, fmt.Errorf("listing workflow node runs: %w", err)
	}
	for rows.Next() {
		var node WorkflowNodeRun
		if err := rows.Scan(&node.NodeID, &node.Name, &node.Type, &node.State, &node.Position, &node.ReadyAt, &node.CompletedAt); err != nil {
			return nil, fmt.Errorf("scanning workflow node run: %w", err)
		}
		run.Nodes = append(run.Nodes, node)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range run.Nodes {
		run.Nodes[i].Attempts, err = d.workflowAttempts(id, run.Nodes[i].NodeID)
		if err != nil {
			return nil, err
		}
	}
	return &run, nil
}

func (d *DB) workflowAttempts(runID, nodeID string) ([]WorkflowAttempt, error) {
	rows, err := d.db.Query(`
		SELECT id, seq, state, started_at, COALESCE(completed_at, 0), exit_code,
		       stdout, stderr, error, outputs_json, stdout_truncated, stderr_truncated,
		       platform, session_id, session_state, affinity, directory
		FROM workflow_node_attempt WHERE run_id = ? AND node_id = ? ORDER BY seq`, runID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("listing workflow attempts: %w", err)
	}
	defer rows.Close()
	var out []WorkflowAttempt
	for rows.Next() {
		var attempt WorkflowAttempt
		if err := rows.Scan(&attempt.ID, &attempt.Seq, &attempt.State, &attempt.StartedAt, &attempt.CompletedAt, &attempt.ExitCode, &attempt.Stdout, &attempt.Stderr, &attempt.Error, &attempt.OutputsJSON, &attempt.StdoutTruncated, &attempt.StderrTruncated, &attempt.Platform, &attempt.SessionID, &attempt.SessionState, &attempt.Affinity, &attempt.Directory); err != nil {
			return nil, fmt.Errorf("scanning workflow attempt: %w", err)
		}
		out = append(out, attempt)
	}
	return out, rows.Err()
}

func (d *DB) StartWorkflowCommand(runID, nodeID string, now int64) (bool, error) {
	result, err := d.db.Exec(`
		UPDATE workflow_node_attempt SET state = 'running', started_at = ?
		WHERE run_id = ? AND node_id = ? AND state = 'waiting'
		AND EXISTS (SELECT 1 FROM workflow_run WHERE id = ? AND state = 'active')`, now, runID, nodeID, runID)
	if err != nil {
		return false, fmt.Errorf("starting workflow command: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (d *DB) CompleteWorkflowCommand(runID, nodeID string, result WorkflowCommandResult, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning command completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var runState, versionID, attemptState string
	if err = tx.QueryRow(`SELECT state, version_id FROM workflow_run WHERE id = ?`, runID).Scan(&runState, &versionID); err != nil {
		return fmt.Errorf("getting workflow run for command completion: %w", err)
	}
	if runState != "active" && runState != "paused" {
		return fmt.Errorf("workflow run is not active or paused")
	}
	if err = tx.QueryRow(`SELECT state FROM workflow_node_attempt WHERE run_id = ? AND node_id = ? AND state = 'running'`, runID, nodeID).Scan(&attemptState); err != nil {
		return fmt.Errorf("getting running workflow attempt: %w", err)
	}
	if _, err = tx.Exec(`
		UPDATE workflow_node_attempt SET state = ?, completed_at = ?, exit_code = ?, stdout = ?, stderr = ?, error = ?,
		       outputs_json = ?, stdout_truncated = ?, stderr_truncated = ?
		WHERE run_id = ? AND node_id = ? AND state = 'running'`,
		result.State, now, result.ExitCode, result.Stdout, result.Stderr, result.Error, result.OutputsJSON,
		result.StdoutTruncated, result.StderrTruncated, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow command attempt: %w", err)
	}
	nodeState := result.State
	if result.State == "denied" || result.State == "errored" {
		nodeState = "failed"
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'ready'`, nodeState, now, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow command node: %w", err)
	}
	if result.State == "canceled" {
		return tx.Commit()
	}
	if result.State != "successful" {
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND state = 'pending'`, now, runID); err != nil {
			return fmt.Errorf("skipping command descendants: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'canceled', completed_at = ? WHERE run_id = ? AND node_id != ? AND state = 'ready'`, now, runID, nodeID); err != nil {
			return fmt.Errorf("canceling active command nodes: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'canceled', completed_at = ? WHERE run_id = ? AND node_id != ? AND state IN ('waiting', 'running')`, now, runID, nodeID); err != nil {
			return fmt.Errorf("canceling other attempts: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'failed', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("failing workflow run: %w", err)
		}
		return tx.Commit()
	}

	var remaining int
	if err = tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state != 'successful'`, runID).Scan(&remaining); err != nil {
		return fmt.Errorf("counting workflow nodes: %w", err)
	}
	if remaining == 0 {
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'successful', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("completing workflow run: %w", err)
		}
		return tx.Commit()
	}
	rows, err := tx.Query(`
		SELECT nr.node_id, n.type FROM workflow_node_run nr
		JOIN workflow_version_node n ON n.version_id = ? AND n.node_id = nr.node_id
		WHERE nr.run_id = ? AND nr.state = 'pending'
		AND NOT EXISTS (
			SELECT 1 FROM workflow_version_dependency d
			JOIN workflow_node_run upstream ON upstream.run_id = nr.run_id AND upstream.node_id = d.from_node
			WHERE d.version_id = ? AND d.to_node = nr.node_id AND upstream.state != 'successful'
		)`, versionID, runID, versionID)
	if err != nil {
		return fmt.Errorf("finding ready workflow nodes: %w", err)
	}
	type readyNode struct{ id, nodeType string }
	var ready []readyNode
	for rows.Next() {
		var node readyNode
		if err = rows.Scan(&node.id, &node.nodeType); err != nil {
			rows.Close()
			return err
		}
		ready = append(ready, node)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, node := range ready {
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ? WHERE run_id = ? AND node_id = ?`, now, runID, node.id); err != nil {
			return fmt.Errorf("readying workflow node: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, runID, node.id, now); err != nil {
			return fmt.Errorf("inserting workflow attempt: %w", err)
		}
	}
	if _, err = tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
		return fmt.Errorf("updating workflow run: %w", err)
	}
	return tx.Commit()
}

func (d *DB) ClaimWorkflowAgentAttempt(runID, nodeID string, attemptID int64, affinity, directory string, now int64) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE workflow_node_attempt SET state = 'starting', affinity = ?, directory = ? WHERE id = ? AND run_id = ? AND node_id = ? AND state = 'waiting'`, affinity, directory, attemptID, runID, nodeID)
	if err != nil {
		return false, fmt.Errorf("claiming workflow agent attempt: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 0 {
		return false, nil
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'running' WHERE run_id = ? AND node_id = ? AND state = 'ready'`, runID, nodeID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (d *DB) AttachWorkflowAgentSession(runID, nodeID string, attemptID int64, platform, sessionID, sessionState string, now int64) error {
	res, err := d.db.Exec(`UPDATE workflow_node_attempt SET state = 'running', platform = ?, session_id = ?, session_state = ? WHERE id = ? AND run_id = ? AND node_id = ? AND state = 'starting'`, platform, sessionID, sessionState, attemptID, runID, nodeID)
	if err != nil {
		return fmt.Errorf("attaching workflow agent session: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("workflow agent attempt is not starting")
	}
	_, err = d.db.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID)
	return err
}

func (d *DB) SetWorkflowAgentSessionState(runID, nodeID string, attemptID int64, sessionState, attemptError string, now int64) error {
	_, err := d.db.Exec(`UPDATE workflow_node_attempt SET session_state = ?, error = ?, completed_at = CASE WHEN ? = 'canceled' THEN ? ELSE completed_at END WHERE id = ? AND run_id = ? AND node_id = ?`, sessionState, attemptError, sessionState, now, attemptID, runID, nodeID)
	if err != nil {
		return fmt.Errorf("updating workflow agent session: %w", err)
	}
	return nil
}

func (d *DB) CompleteWorkflowAgentNode(runID, nodeID string, attemptID int64, successful bool, sessionState, outputsJSON, attemptError string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow agent completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	nodeState, attemptState, runState := "failed", "failed", "failed"
	if successful {
		nodeState, attemptState, runState = "successful", "successful", "successful"
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'running'`, nodeState, now, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow agent node: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = ?, session_state = ?, outputs_json = ?, error = ?, completed_at = ? WHERE id = ? AND state IN ('starting', 'running')`, attemptState, sessionState, outputsJSON, attemptError, now, attemptID); err != nil {
		return fmt.Errorf("completing workflow agent attempt: %w", err)
	}
	if !successful {
		if _, err = tx.Exec(`UPDATE workflow_run SET state = ?, updated_at = ?, completed_at = ? WHERE id = ?`, runState, now, now, runID); err != nil {
			return fmt.Errorf("failing workflow run: %w", err)
		}
		return tx.Commit()
	}
	return completeWorkflowNodeTx(tx, runID, now)
}

func completeWorkflowNodeTx(tx *sql.Tx, runID string, now int64) error {
	var remaining int
	if err := tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state != 'successful'`, runID).Scan(&remaining); err != nil {
		return fmt.Errorf("counting workflow nodes: %w", err)
	}
	if remaining == 0 {
		if _, err := tx.Exec(`UPDATE workflow_run SET state = 'successful', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("completing workflow run: %w", err)
		}
		return tx.Commit()
	}
	var versionID string
	if err := tx.QueryRow(`SELECT version_id FROM workflow_run WHERE id = ?`, runID).Scan(&versionID); err != nil {
		return err
	}
	rows, err := tx.Query(`
		SELECT nr.node_id FROM workflow_node_run nr
		WHERE nr.run_id = ? AND nr.state = 'pending'
		AND NOT EXISTS (
			SELECT 1 FROM workflow_version_dependency d
			JOIN workflow_node_run upstream ON upstream.run_id = nr.run_id AND upstream.node_id = d.from_node
			WHERE d.version_id = ? AND d.to_node = nr.node_id AND upstream.state != 'successful'
		)`, runID, versionID)
	if err != nil {
		return fmt.Errorf("finding ready workflow nodes: %w", err)
	}
	var ready []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ready = append(ready, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ready {
		if _, err := tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ? WHERE run_id = ? AND node_id = ?`, now, runID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, runID, id, now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) ApproveWorkflowNode(runID, nodeID string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow approval: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var state, versionID string
	if err = tx.QueryRow(`SELECT state, version_id FROM workflow_run WHERE id = ?`, runID).Scan(&state, &versionID); err != nil {
		return fmt.Errorf("getting workflow run for approval: %w", err)
	}
	if state != "active" {
		return fmt.Errorf("workflow run is not active")
	}
	var nodeState, nodeType string
	if err = tx.QueryRow(`
		SELECT nr.state, n.type FROM workflow_node_run nr
		JOIN workflow_version_node n ON n.version_id = ? AND n.node_id = nr.node_id
		WHERE nr.run_id = ? AND nr.node_id = ?`, versionID, runID, nodeID).Scan(&nodeState, &nodeType); err != nil {
		return fmt.Errorf("getting workflow node for approval: %w", err)
	}
	if nodeType != "approval" {
		return fmt.Errorf("workflow node %q is not an approval", nodeID)
	}
	if nodeState != "ready" {
		return fmt.Errorf("workflow node %q is not ready", nodeID)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'successful', completed_at = ? WHERE run_id = ? AND node_id = ?`, now, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow node: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'successful', completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'waiting'`, now, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow attempt: %w", err)
	}
	var remaining int
	if err = tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state != 'successful'`, runID).Scan(&remaining); err != nil {
		return fmt.Errorf("counting workflow nodes: %w", err)
	}
	if remaining == 0 {
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'successful', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("completing workflow run: %w", err)
		}
	} else {
		rows, queryErr := tx.Query(`
			SELECT nr.node_id, n.type FROM workflow_node_run nr
			JOIN workflow_version_node n ON n.version_id = ? AND n.node_id = nr.node_id
			WHERE nr.run_id = ? AND nr.state = 'pending'
			AND NOT EXISTS (
				SELECT 1 FROM workflow_version_dependency d
				JOIN workflow_node_run upstream ON upstream.run_id = nr.run_id AND upstream.node_id = d.from_node
				WHERE d.version_id = ? AND d.to_node = nr.node_id AND upstream.state != 'successful'
			)`, versionID, runID, versionID)
		if queryErr != nil {
			return fmt.Errorf("finding ready workflow nodes: %w", queryErr)
		}
		type readyNode struct{ id, nodeType string }
		var ready []readyNode
		for rows.Next() {
			var node readyNode
			if err = rows.Scan(&node.id, &node.nodeType); err != nil {
				rows.Close()
				return fmt.Errorf("scanning ready workflow node: %w", err)
			}
			ready = append(ready, node)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, node := range ready {
			if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ? WHERE run_id = ? AND node_id = ?`, now, runID, node.id); err != nil {
				return fmt.Errorf("readying workflow node: %w", err)
			}
			if _, err = tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, runID, node.id, now); err != nil {
				return fmt.Errorf("inserting workflow attempt: %w", err)
			}
		}
		if _, err = tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
			return fmt.Errorf("updating workflow run: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow approval: %w", err)
	}
	return nil
}

func (d *DB) SetWorkflowRunState(id, from, to string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow state change: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	completed := interface{}(nil)
	if to == "canceled" {
		completed = now
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'canceled', completed_at = ? WHERE run_id = ? AND state IN ('pending', 'ready', 'running')`, now, id); err != nil {
			return fmt.Errorf("canceling workflow nodes: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'canceled', session_state = CASE WHEN session_id != '' THEN 'canceled' ELSE session_state END, completed_at = ? WHERE run_id = ? AND state IN ('waiting', 'starting', 'running')`, now, id); err != nil {
			return fmt.Errorf("canceling workflow attempts: %w", err)
		}
	}
	res, err := tx.Exec(`UPDATE workflow_run SET state = ?, updated_at = ?, completed_at = ? WHERE id = ? AND state = ?`, to, now, completed, id, from)
	if err != nil {
		return fmt.Errorf("setting workflow run state: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("workflow run cannot transition from %s to %s", from, to)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing workflow state change: %w", err)
	}
	return nil
}

func (d *DB) ResolveWorkflowAttempt(runID string, attemptID int64, resolution string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning unknown attempt resolution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var nodeID, attemptState, runState string
	err = tx.QueryRow(`
		SELECT a.node_id, a.state, r.state
		FROM workflow_node_attempt a
		JOIN workflow_run r ON r.id = a.run_id
		WHERE a.run_id = ? AND a.id = ?`, runID, attemptID).Scan(&nodeID, &attemptState, &runState)
	if err != nil || attemptState != "unknown" {
		return fmt.Errorf("unknown attempt %d not found for workflow run %q", attemptID, runID)
	}
	if runState != "paused" {
		return fmt.Errorf("workflow run must be paused to resolve an unknown attempt")
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = ?, completed_at = ? WHERE id = ?`, resolution, now, attemptID); err != nil {
		return fmt.Errorf("updating workflow attempt: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ?`, resolution, now, runID, nodeID); err != nil {
		return fmt.Errorf("updating workflow node: %w", err)
	}
	if resolution == "failed" {
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'failed', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("failing workflow run: %w", err)
		}
	} else {
		var remaining int
		if err = tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state != 'successful'`, runID).Scan(&remaining); err != nil {
			return fmt.Errorf("counting workflow nodes: %w", err)
		}
		if remaining == 0 {
			if _, err = tx.Exec(`UPDATE workflow_run SET state = 'successful', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
				return fmt.Errorf("completing workflow run: %w", err)
			}
		} else if _, err = tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
			return fmt.Errorf("updating workflow run: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing unknown attempt resolution: %w", err)
	}
	return nil
}
