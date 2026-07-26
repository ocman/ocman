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
	RetentionDays   int
	CreatedAt       int64
	Active          bool
	Nodes           []WorkflowNode
	Dependencies    []WorkflowDependency
}

type WorkflowNode struct {
	ID            string
	Name          string
	Type          string
	Position      int
	SubworkflowID string
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
	ParentRunID         string
	ParentNodeID        string
	ItemKey             string
	ItemIndex           int
	RetryOfRunID        string
	RetryFromNodeID     string
	Nodes               []WorkflowNodeRun
}

type WorkflowNodeRun struct {
	NodeID          string
	Name            string
	Type            string
	State           string
	Position        int
	ReadyAt         int64
	CompletedAt     int64
	PinnedVersionID string
	Attempts        []WorkflowAttempt
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
	ResolvedAt      int64
	ResolvedBy      string
	ReusedAttemptID int64
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

type WorkflowAgentResult struct {
	Successful   bool
	SessionState string
	Output       string
	OutputsJSON  string
	Error        string
}

// WorkflowResourceRequest is one pool acquisition an attempt needs. Pool
// "" is the implicit run-concurrency cap. Capacity is the pool's total.
type WorkflowResourceRequest struct {
	Pool     string
	Units    int
	Capacity int
}

// WorkflowResourceLease is a durable record of held capacity, used for
// restart reconciliation and UI visibility.
type WorkflowResourceLease struct {
	RunID      string
	NodeID     string
	AttemptID  int64
	Pool       string
	Units      int
	AcquiredAt int64
}

// acquireWorkflowResources atomically holds every requested unit within
// tx, failing (rolling back to the caller) if any pool lacks capacity.
// Idempotent per (run, node, pool): a re-acquire keeps the existing lease
// row rather than double-counting. Returns false without error when the
// run is out of capacity for at least one requested pool.
func acquireWorkflowResources(tx *sql.Tx, runID, nodeID string, attemptID int64, requests []WorkflowResourceRequest, now int64) (bool, error) {
	for _, req := range requests {
		if req.Units <= 0 {
			continue
		}
		var held int
		if err := tx.QueryRow(`SELECT COALESCE(SUM(units), 0) FROM workflow_resource_lease WHERE run_id = ? AND pool = ? AND node_id != ?`, runID, req.Pool, nodeID).Scan(&held); err != nil {
			return false, fmt.Errorf("summing held resources: %w", err)
		}
		if held+req.Units > req.Capacity {
			return false, nil
		}
	}
	for _, req := range requests {
		if req.Units <= 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO workflow_resource_lease (run_id, node_id, attempt_id, pool, units, acquired_at) VALUES (?, ?, ?, ?, ?, ?)`, runID, nodeID, attemptID, req.Pool, req.Units, now); err != nil {
			return false, fmt.Errorf("holding resource: %w", err)
		}
	}
	return true, nil
}

// releaseWorkflowResources drops every held resource and workspace lease
// for a settled node within tx, so waiting siblings can acquire the freed
// pool capacity and shard.
func releaseWorkflowResources(exec workflowRunExecer, runID, nodeID string) error {
	if _, err := exec.Exec(`DELETE FROM workflow_resource_lease WHERE run_id = ? AND node_id = ?`, runID, nodeID); err != nil {
		return fmt.Errorf("releasing resources: %w", err)
	}
	if err := releaseWorkflowWorkspaceLease(exec, runID, nodeID); err != nil {
		return err
	}
	return nil
}

// deleteAllWorkflowLeases drops every resource and workspace lease held by
// a run within tx, used when a run terminates (failure, cancel, budget).
func deleteAllWorkflowLeases(exec workflowRunExecer, runID string) error {
	if _, err := exec.Exec(`DELETE FROM workflow_resource_lease WHERE run_id = ?`, runID); err != nil {
		return fmt.Errorf("releasing resources: %w", err)
	}
	if _, err := exec.Exec(`DELETE FROM workflow_workspace_lease WHERE run_id = ?`, runID); err != nil {
		return fmt.Errorf("releasing workspace leases: %w", err)
	}
	return nil
}

// ListWorkflowResourceLeases returns held capacity for a run, for UI and
// restart reconciliation.
func (d *DB) ListWorkflowResourceLeases(runID string) ([]WorkflowResourceLease, error) {
	rows, err := d.db.Query(`SELECT run_id, node_id, attempt_id, pool, units, acquired_at FROM workflow_resource_lease WHERE run_id = ? ORDER BY pool, node_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing resource leases: %w", err)
	}
	defer rows.Close()
	var out []WorkflowResourceLease
	for rows.Next() {
		var lease WorkflowResourceLease
		if err := rows.Scan(&lease.RunID, &lease.NodeID, &lease.AttemptID, &lease.Pool, &lease.Units, &lease.AcquiredAt); err != nil {
			return nil, fmt.Errorf("scanning resource lease: %w", err)
		}
		out = append(out, lease)
	}
	return out, rows.Err()
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
	if _, err = tx.Exec(`INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, retention_days, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.ID, v.WorkflowID, v.Name, v.Revision, v.MetadataVersion, v.DefinitionJSON, v.Concurrency, v.RetentionDays, v.CreatedAt); err != nil {
		return WorkflowVersion{}, fmt.Errorf("inserting workflow version: %w", err)
	}
	for _, node := range v.Nodes {
		if _, err = tx.Exec(`INSERT INTO workflow_version_node (version_id, node_id, name, type, position, subworkflow_id) VALUES (?, ?, ?, ?, ?, ?)`, v.ID, node.ID, node.Name, node.Type, node.Position, node.SubworkflowID); err != nil {
			return WorkflowVersion{}, fmt.Errorf("inserting workflow node: %w", err)
		}
	}
	for _, dep := range v.Dependencies {
		if _, err = tx.Exec(`INSERT INTO workflow_version_dependency (version_id, from_node, to_node) VALUES (?, ?, ?)`, v.ID, dep.From, dep.To); err != nil {
			return WorkflowVersion{}, fmt.Errorf("inserting workflow dependency: %w", err)
		}
	}
	if v.Revision == 1 {
		if _, err = tx.Exec(`UPDATE workflow_definition SET active_version_id = ? WHERE id = ?`, v.ID, v.WorkflowID); err != nil {
			return WorkflowVersion{}, fmt.Errorf("activating initial workflow version: %w", err)
		}
		v.Active = true
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
		       v.definition_json, v.concurrency, v.retention_days, v.created_at,
		       COALESCE(d.active_version_id = v.id, 0)
		FROM workflow_version v
		JOIN workflow_definition d ON d.id = v.workflow_id
		WHERE v.id = ?`, id).Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.RetentionDays, &v.CreatedAt, &v.Active)
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

func (d *DB) ListWorkflowVersions() ([]WorkflowVersion, error) {
	rows, err := d.db.Query(`
		SELECT v.id, v.workflow_id, v.name, v.revision, v.metadata_version,
		       v.definition_json, v.concurrency, v.retention_days, v.created_at,
		       COALESCE(d.active_version_id = v.id, 0)
		FROM workflow_version v
		JOIN workflow_definition d ON d.id = v.workflow_id
		WHERE d.archived_at IS NULL
		ORDER BY v.created_at DESC, v.revision DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing workflow versions: %w", err)
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		var v WorkflowVersion
		if err := rows.Scan(&v.ID, &v.WorkflowID, &v.Name, &v.Revision, &v.MetadataVersion, &v.DefinitionJSON, &v.Concurrency, &v.RetentionDays, &v.CreatedAt, &v.Active); err != nil {
			return nil, fmt.Errorf("scanning workflow version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) ArchiveWorkflowVersion(id string, now int64) error {
	res, err := d.db.Exec(`UPDATE workflow_definition SET archived_at = ?, active_version_id = NULL, updated_at = ? WHERE id = (SELECT workflow_id FROM workflow_version WHERE id = ?) AND archived_at IS NULL`, now, now, id)
	if err != nil {
		return fmt.Errorf("archiving workflow version: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("workflow version %q not found", id)
	}
	return nil
}

func (d *DB) workflowVersionNodes(id string) ([]WorkflowNode, error) {
	rows, err := d.db.Query(`SELECT node_id, name, type, position, subworkflow_id FROM workflow_version_node WHERE version_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("listing workflow nodes: %w", err)
	}
	defer rows.Close()
	var out []WorkflowNode
	for rows.Next() {
		var node WorkflowNode
		if err := rows.Scan(&node.ID, &node.Name, &node.Type, &node.Position, &node.SubworkflowID); err != nil {
			return nil, fmt.Errorf("scanning workflow node: %w", err)
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (d *DB) ActivateWorkflowVersion(id string, now int64) (*WorkflowVersion, error) {
	res, err := d.db.Exec(`UPDATE workflow_definition SET active_version_id = ?, updated_at = ? WHERE id = (SELECT workflow_id FROM workflow_version WHERE id = ?)`, id, now, id)
	if err != nil {
		return nil, fmt.Errorf("activating workflow version: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return nil, fmt.Errorf("workflow version %q not found", id)
	}
	return d.GetWorkflowVersion(id)
}

func (d *DB) DeactivateWorkflowVersion(id string, now int64) (*WorkflowVersion, error) {
	res, err := d.db.Exec(`UPDATE workflow_definition SET active_version_id = NULL, updated_at = ? WHERE active_version_id = ?`, now, id)
	if err != nil {
		return nil, fmt.Errorf("deactivating workflow version: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("checking workflow version deactivation: %w", err)
	}
	if changed == 0 {
		return nil, sql.ErrNoRows
	}
	return d.GetWorkflowVersion(id)
}

func (d *DB) GetActiveWorkflowVersion(workflowID string) (*WorkflowVersion, error) {
	var id string
	if err := d.db.QueryRow(`SELECT active_version_id FROM workflow_definition WHERE id = ? AND active_version_id IS NOT NULL`, workflowID).Scan(&id); err != nil {
		return nil, fmt.Errorf("getting active workflow version: %w", err)
	}
	return d.GetWorkflowVersion(id)
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
	if _, err := exec.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at, trigger_snapshot_json, parent_run_id, parent_node_id, item_key, item_index, retry_of_run_id, retry_from_node_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, run.WorkflowID, run.VersionID, run.State, run.CreatedAt, run.UpdatedAt, nullableString(run.TriggerSnapshotJSON), nullableString(run.ParentRunID), nullableString(run.ParentNodeID), nullableString(run.ItemKey), nullableInt(int64(run.ItemIndex)), nullableString(run.RetryOfRunID), nullableString(run.RetryFromNodeID)); err != nil {
		return fmt.Errorf("inserting workflow run: %w", err)
	}
	for _, node := range run.Nodes {
		if _, err := exec.Exec(`INSERT INTO workflow_node_run (run_id, node_id, state, position, ready_at, pinned_version_id) VALUES (?, ?, ?, ?, ?, ?)`, run.ID, node.NodeID, node.State, node.Position, nullableInt(node.ReadyAt), nullableString(node.PinnedVersionID)); err != nil {
			return fmt.Errorf("inserting workflow node run: %w", err)
		}
		if node.State == "ready" {
			if _, err := exec.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, run.ID, node.NodeID, run.CreatedAt); err != nil {
				return fmt.Errorf("inserting approval attempt: %w", err)
			}
		}
		for _, attempt := range node.Attempts {
			if _, err := exec.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at, completed_at, exit_code, stdout, stderr, error, outputs_json, stdout_truncated, stderr_truncated, platform, session_id, session_state, affinity, directory, resolved_at, resolved_by, reused_attempt_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, node.NodeID, attempt.Seq, attempt.State, attempt.StartedAt, nullableInt(attempt.CompletedAt), attempt.ExitCode, attempt.Stdout, attempt.Stderr, attempt.Error, attempt.OutputsJSON, attempt.StdoutTruncated, attempt.StderrTruncated, attempt.Platform, attempt.SessionID, attempt.SessionState, attempt.Affinity, attempt.Directory, nullableInt(attempt.ResolvedAt), attempt.ResolvedBy, nullableInt(attempt.ReusedAttemptID)); err != nil {
				return fmt.Errorf("inserting reused workflow attempt: %w", err)
			}
		}
	}
	return nil
}

func (d *DB) ListWorkflowRuns() ([]WorkflowRun, error) {
	rows, err := d.db.Query(`SELECT id, workflow_id, version_id, state, created_at, updated_at, COALESCE(completed_at, 0), COALESCE(trigger_snapshot_json, ''), COALESCE(parent_run_id, ''), COALESCE(parent_node_id, ''), COALESCE(item_key, ''), COALESCE(item_index, 0), COALESCE(retry_of_run_id, ''), COALESCE(retry_from_node_id, '') FROM workflow_run ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing workflow runs: %w", err)
	}
	defer rows.Close()
	var out []WorkflowRun
	for rows.Next() {
		var run WorkflowRun
		if err := rows.Scan(&run.ID, &run.WorkflowID, &run.VersionID, &run.State, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt, &run.TriggerSnapshotJSON, &run.ParentRunID, &run.ParentNodeID, &run.ItemKey, &run.ItemIndex, &run.RetryOfRunID, &run.RetryFromNodeID); err != nil {
			return nil, fmt.Errorf("scanning workflow run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ListWorkflowChildRuns returns the child (mapped-item) runs of a parent
// run, ordered by their input item index.
func (d *DB) ListWorkflowChildRuns(parentRunID string) ([]WorkflowRun, error) {
	rows, err := d.db.Query(`SELECT id, workflow_id, version_id, state, created_at, updated_at, COALESCE(completed_at, 0), COALESCE(trigger_snapshot_json, ''), COALESCE(parent_run_id, ''), COALESCE(parent_node_id, ''), COALESCE(item_key, ''), COALESCE(item_index, 0), COALESCE(retry_of_run_id, ''), COALESCE(retry_from_node_id, '') FROM workflow_run WHERE parent_run_id = ? ORDER BY parent_node_id, item_index`, parentRunID)
	if err != nil {
		return nil, fmt.Errorf("listing workflow child runs: %w", err)
	}
	defer rows.Close()
	var out []WorkflowRun
	for rows.Next() {
		var run WorkflowRun
		if err := rows.Scan(&run.ID, &run.WorkflowID, &run.VersionID, &run.State, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt, &run.TriggerSnapshotJSON, &run.ParentRunID, &run.ParentNodeID, &run.ItemKey, &run.ItemIndex, &run.RetryOfRunID, &run.RetryFromNodeID); err != nil {
			return nil, fmt.Errorf("scanning workflow child run: %w", err)
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (d *DB) GetWorkflowRun(id string) (*WorkflowRun, error) {
	var run WorkflowRun
	err := d.db.QueryRow(`SELECT id, workflow_id, version_id, state, created_at, updated_at, COALESCE(completed_at, 0), COALESCE(trigger_snapshot_json, ''), COALESCE(parent_run_id, ''), COALESCE(parent_node_id, ''), COALESCE(item_key, ''), COALESCE(item_index, 0), COALESCE(retry_of_run_id, ''), COALESCE(retry_from_node_id, '') FROM workflow_run WHERE id = ?`, id).Scan(&run.ID, &run.WorkflowID, &run.VersionID, &run.State, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt, &run.TriggerSnapshotJSON, &run.ParentRunID, &run.ParentNodeID, &run.ItemKey, &run.ItemIndex, &run.RetryOfRunID, &run.RetryFromNodeID)
	if err != nil {
		return nil, fmt.Errorf("getting workflow run: %w", err)
	}
	rows, err := d.db.Query(`
		SELECT nr.node_id, n.name, n.type, nr.state, nr.position,
		       COALESCE(nr.ready_at, 0), COALESCE(nr.completed_at, 0), COALESCE(nr.pinned_version_id, '')
		FROM workflow_node_run nr
		JOIN workflow_version_node n ON n.version_id = ? AND n.node_id = nr.node_id
		WHERE nr.run_id = ? ORDER BY nr.position`, run.VersionID, id)
	if err != nil {
		return nil, fmt.Errorf("listing workflow node runs: %w", err)
	}
	for rows.Next() {
		var node WorkflowNodeRun
		if err := rows.Scan(&node.NodeID, &node.Name, &node.Type, &node.State, &node.Position, &node.ReadyAt, &node.CompletedAt, &node.PinnedVersionID); err != nil {
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
		       platform, session_id, session_state, affinity, directory,
		       COALESCE(resolved_at, 0), resolved_by, COALESCE(reused_attempt_id, 0)
		FROM workflow_node_attempt WHERE run_id = ? AND node_id = ? ORDER BY seq`, runID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("listing workflow attempts: %w", err)
	}
	defer rows.Close()
	var out []WorkflowAttempt
	for rows.Next() {
		var attempt WorkflowAttempt
		if err := rows.Scan(&attempt.ID, &attempt.Seq, &attempt.State, &attempt.StartedAt, &attempt.CompletedAt, &attempt.ExitCode, &attempt.Stdout, &attempt.Stderr, &attempt.Error, &attempt.OutputsJSON, &attempt.StdoutTruncated, &attempt.StderrTruncated, &attempt.Platform, &attempt.SessionID, &attempt.SessionState, &attempt.Affinity, &attempt.Directory, &attempt.ResolvedAt, &attempt.ResolvedBy, &attempt.ReusedAttemptID); err != nil {
			return nil, fmt.Errorf("scanning workflow attempt: %w", err)
		}
		out = append(out, attempt)
	}
	return out, rows.Err()
}

func (d *DB) StartWorkflowCommand(runID, nodeID string, requests []WorkflowResourceRequest, workspace *WorkflowWorkspaceRequest, now int64) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning workflow command start: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var attemptID int64
	err = tx.QueryRow(`
		SELECT a.id FROM workflow_node_attempt a
		WHERE a.run_id = ? AND a.node_id = ? AND a.state = 'waiting'
		AND EXISTS (SELECT 1 FROM workflow_run WHERE id = ? AND state = 'active')`, runID, nodeID, runID).Scan(&attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("finding waiting workflow command: %w", err)
	}
	acquired, err := acquireWorkflowResources(tx, runID, nodeID, attemptID, requests, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	if workspace != nil {
		if _, ok, err := acquireWorkflowWorkspaceLease(tx, runID, nodeID, attemptID, *workspace, now); err != nil {
			return false, err
		} else if !ok {
			return false, nil
		}
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'running', started_at = ? WHERE id = ?`, now, attemptID); err != nil {
		return false, fmt.Errorf("starting workflow command: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("committing workflow command start: %w", err)
	}
	return true, nil
}

func (d *DB) CompleteWorkflowCommand(runID, nodeID string, result WorkflowCommandResult, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning command completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var runState, attemptState string
	if err = tx.QueryRow(`SELECT state FROM workflow_run WHERE id = ?`, runID).Scan(&runState); err != nil {
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
	// The settled attempt releases its held capacity so waiting siblings
	// can acquire it.
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
	}
	if result.State == "canceled" {
		return tx.Commit()
	}
	if result.State != "successful" {
		return completeWorkflowNodeTx(tx, runID, now)
	}

	return completeWorkflowNodeTx(tx, runID, now)
}

func (d *DB) ClaimWorkflowAgentAttempt(runID, nodeID string, attemptID int64, affinity, directory string, requests []WorkflowResourceRequest, workspace *WorkflowWorkspaceRequest, now int64) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var attemptState string
	err = tx.QueryRow(`SELECT state FROM workflow_node_attempt WHERE id = ? AND run_id = ? AND node_id = ?`, attemptID, runID, nodeID).Scan(&attemptState)
	if errors.Is(err, sql.ErrNoRows) || attemptState != "waiting" {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading workflow agent attempt: %w", err)
	}
	acquired, err := acquireWorkflowResources(tx, runID, nodeID, attemptID, requests, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	if workspace != nil {
		if _, ok, err := acquireWorkflowWorkspaceLease(tx, runID, nodeID, attemptID, *workspace, now); err != nil {
			return false, err
		} else if !ok {
			return false, nil
		}
	}
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

func (d *DB) CompleteWorkflowAgentNode(runID, nodeID string, attemptID int64, result WorkflowAgentResult, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow agent completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	nodeState, attemptState := "failed", "failed"
	if result.Successful {
		nodeState, attemptState = "successful", "successful"
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'running'`, nodeState, now, runID, nodeID); err != nil {
		return fmt.Errorf("completing workflow agent node: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = ?, session_state = ?, stdout = ?, outputs_json = ?, error = ?, completed_at = ? WHERE id = ? AND state IN ('starting', 'running')`, attemptState, result.SessionState, result.Output, result.OutputsJSON, result.Error, now, attemptID); err != nil {
		return fmt.Errorf("completing workflow agent attempt: %w", err)
	}
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
	}
	if !result.Successful {
		return completeWorkflowNodeTx(tx, runID, now)
	}
	return completeWorkflowNodeTx(tx, runID, now)
}

func completeWorkflowNodeTx(tx *sql.Tx, runID string, now int64) error {
	// A failed branch only makes its descendants unreachable. Independent
	// branches keep running; failFast is enforced by the workflow service.
	for {
		res, err := tx.Exec(`UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND state = 'pending' AND EXISTS (
			SELECT 1 FROM workflow_version_dependency d JOIN workflow_run r ON r.id = workflow_node_run.run_id JOIN workflow_node_run upstream ON upstream.run_id = r.id AND upstream.node_id = d.from_node
			WHERE d.version_id = r.version_id AND d.to_node = workflow_node_run.node_id AND upstream.state IN ('failed', 'skipped', 'canceled')
		)`, now, runID)
		if err != nil {
			return fmt.Errorf("skipping unreachable workflow nodes: %w", err)
		}
		changed, err := res.RowsAffected()
		if err != nil || changed == 0 {
			break
		}
	}
	var remaining int
	if err := tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state IN ('pending', 'ready', 'running')`, runID).Scan(&remaining); err != nil {
		return fmt.Errorf("counting workflow nodes: %w", err)
	}
	if remaining == 0 {
		var failed int
		if err := tx.QueryRow(`SELECT count(*) FROM workflow_node_run WHERE run_id = ? AND state = 'failed'`, runID).Scan(&failed); err != nil {
			return err
		}
		state := "successful"
		if failed > 0 {
			state = "failed"
		}
		if _, err := tx.Exec(`UPDATE workflow_run SET state = ?, updated_at = ?, completed_at = ? WHERE id = ?`, state, now, now, runID); err != nil {
			return fmt.Errorf("settling workflow run: %w", err)
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

// FailWorkflowRun terminates an active/paused run: it cancels every
// non-terminal node and attempt, records reason on their unfinished
// attempts, releases all held resources, and marks the run failed. Used
// for configured budget stops. No-op (nil) if the run already settled.
func (d *DB) FailWorkflowRun(id, reason string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning workflow budget fail: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var runState string
	if err = tx.QueryRow(`SELECT state FROM workflow_run WHERE id = ?`, id).Scan(&runState); err != nil {
		return fmt.Errorf("getting workflow run: %w", err)
	}
	if runState != "active" && runState != "paused" {
		return nil
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'canceled', error = CASE WHEN error = '' THEN ? ELSE error END, session_state = CASE WHEN session_id != '' THEN 'canceled' ELSE session_state END, completed_at = ? WHERE run_id = ? AND state IN ('waiting', 'starting', 'running')`, reason, now, id); err != nil {
		return fmt.Errorf("canceling attempts on budget fail: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND state = 'pending'`, now, id); err != nil {
		return fmt.Errorf("skipping unreachable nodes on budget fail: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'canceled', completed_at = ? WHERE run_id = ? AND state IN ('ready', 'running')`, now, id); err != nil {
		return fmt.Errorf("canceling nodes on budget fail: %w", err)
	}
	if err = deleteAllWorkflowLeases(tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE workflow_run SET state = 'failed', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, id); err != nil {
		return fmt.Errorf("failing workflow run: %w", err)
	}
	return tx.Commit()
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
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND state = 'pending'`, now, id); err != nil {
			return fmt.Errorf("skipping unreachable workflow nodes: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'canceled', completed_at = ? WHERE run_id = ? AND state IN ('ready', 'running')`, now, id); err != nil {
			return fmt.Errorf("canceling workflow nodes: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'canceled', session_state = CASE WHEN session_id != '' THEN 'canceled' ELSE session_state END, completed_at = ? WHERE run_id = ? AND state IN ('waiting', 'starting', 'running')`, now, id); err != nil {
			return fmt.Errorf("canceling workflow attempts: %w", err)
		}
		if err = deleteAllWorkflowLeases(tx, id); err != nil {
			return err
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
	return d.ResolveWorkflowAttemptBy(runID, attemptID, resolution, "user", now)
}

// ResolveWorkflowAttemptBy resolves an unknown attempt with an audit record
// (resolvedBy + timestamp). "successful" and "failed" settle the attempt and
// run; "retry" marks the unknown attempt as failed, creates a new waiting
// attempt, sets the node back to ready, and resumes the run so the scheduler
// can dispatch the retry under existing limits.
func (d *DB) ResolveWorkflowAttemptBy(runID string, attemptID int64, resolution, resolvedBy string, now int64) error {
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
	if resolution == "retry" {
		if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'failed', completed_at = ?, resolved_at = ?, resolved_by = ? WHERE id = ?`, now, now, resolvedBy, attemptID); err != nil {
			return fmt.Errorf("marking unknown attempt as failed for retry: %w", err)
		}
		if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
			return err
		}
		var nextSeq int
		if err = tx.QueryRow(`SELECT COALESCE(max(seq), 0) + 1 FROM workflow_node_attempt WHERE run_id = ? AND node_id = ?`, runID, nodeID).Scan(&nextSeq); err != nil {
			return fmt.Errorf("computing retry attempt seq: %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, ?, 'waiting', ?)`, runID, nodeID, nextSeq, now); err != nil {
			return fmt.Errorf("creating retry attempt: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ?, completed_at = NULL WHERE run_id = ? AND node_id = ?`, now, runID, nodeID); err != nil {
			return fmt.Errorf("readying node for retry: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'active', updated_at = ? WHERE id = ?`, now, runID); err != nil {
			return fmt.Errorf("resuming workflow run for retry: %w", err)
		}
		return tx.Commit()
	}
	if resolution != "successful" && resolution != "failed" {
		return fmt.Errorf("resolution must be %q, %q, or %q", "successful", "failed", "retry")
	}
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = ?, completed_at = ?, resolved_at = ?, resolved_by = ? WHERE id = ?`, resolution, now, now, resolvedBy, attemptID); err != nil {
		return fmt.Errorf("updating workflow attempt: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ?`, resolution, now, runID, nodeID); err != nil {
		return fmt.Errorf("updating workflow node: %w", err)
	}
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
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
		} else {
			var versionID string
			if err = tx.QueryRow(`SELECT version_id FROM workflow_run WHERE id = ?`, runID).Scan(&versionID); err != nil {
				return fmt.Errorf("getting workflow version for resolution: %w", err)
			}
			rows, queryErr := tx.Query(`
				SELECT nr.node_id FROM workflow_node_run nr
				WHERE nr.run_id = ? AND nr.state = 'pending'
				AND NOT EXISTS (
					SELECT 1 FROM workflow_version_dependency d
					JOIN workflow_node_run upstream ON upstream.run_id = nr.run_id AND upstream.node_id = d.from_node
					WHERE d.version_id = ? AND d.to_node = nr.node_id AND upstream.state != 'successful'
				)`, runID, versionID)
			if queryErr != nil {
				return fmt.Errorf("finding nodes unblocked by resolution: %w", queryErr)
			}
			var ready []string
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ready = append(ready, id)
			}
			if err = rows.Close(); err != nil {
				return err
			}
			for _, id := range ready {
				if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ? WHERE run_id = ? AND node_id = ?`, now, runID, id); err != nil {
					return fmt.Errorf("readying node after resolution: %w", err)
				}
				if _, err = tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, 1, 'waiting', ?)`, runID, id, now); err != nil {
					return fmt.Errorf("creating attempt after resolution: %w", err)
				}
			}
			if _, err = tx.Exec(`UPDATE workflow_run SET state = 'active', updated_at = ? WHERE id = ?`, now, runID); err != nil {
				return fmt.Errorf("resuming workflow run: %w", err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing unknown attempt resolution: %w", err)
	}
	return nil
}

// MarkWorkflowAttemptUnknown transitions a running/starting attempt to
// unknown (uncertain side effects after a restart), sets the node to
// unknown, releases the attempt's held resource and workspace leases so
// they can be re-acquired if the user resolves for retry, and pauses the
// run so dependent scheduling stops until a human decision.
func (d *DB) MarkWorkflowAttemptUnknown(runID, nodeID string, attemptID int64, reason string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning unknown attempt transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE workflow_node_attempt SET state = 'unknown', error = ?, completed_at = ? WHERE id = ? AND state IN ('starting', 'running')`, reason, now, attemptID)
	if err != nil {
		return fmt.Errorf("marking attempt unknown: %w", err)
	}
	// The caller decides to mark an attempt unknown from a snapshot read
	// without the dispatch lock, then does network I/O before writing —
	// so the attempt can settle in between. Only the attempt update was
	// guarded, so a settled attempt still flipped its node to unknown and
	// paused the run: ResolveWorkflowAttemptBy then rejects the node
	// (attempt is not 'unknown') and RetryFrom needs successful/failed,
	// leaving it stuck on a paused run that only cancelling can clear.
	if changed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("marking attempt unknown: %w", err)
	} else if changed == 0 {
		return nil
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'unknown' WHERE run_id = ? AND node_id = ?`, runID, nodeID); err != nil {
		return fmt.Errorf("marking node unknown: %w", err)
	}
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE workflow_run SET state = 'paused', updated_at = ? WHERE id = ? AND state = 'active'`, now, runID); err != nil {
		return fmt.Errorf("pausing workflow run: %w", err)
	}
	return tx.Commit()
}

// RetryWorkflowAttempt marks an interrupted attempt as failed with an audit
// record, releases its held leases, creates a new waiting attempt for the
// same node, and sets the node back to ready. Used for retry-safe recovery
// (e.g. agent launch interrupted before any side effect). The run stays
// active so the scheduler dispatches the new attempt on the next tick.
func (d *DB) RetryWorkflowAttempt(runID, nodeID string, attemptID int64, reason, resolvedBy string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning retry-safe recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'failed', error = ?, completed_at = ?, resolved_at = ?, resolved_by = ? WHERE id = ? AND state IN ('starting', 'running')`, reason, now, now, resolvedBy, attemptID); err != nil {
		return fmt.Errorf("marking interrupted attempt failed: %w", err)
	}
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
	}
	var nextSeq int
	if err = tx.QueryRow(`SELECT COALESCE(max(seq), 0) + 1 FROM workflow_node_attempt WHERE run_id = ? AND node_id = ?`, runID, nodeID).Scan(&nextSeq); err != nil {
		return fmt.Errorf("computing retry attempt seq: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at) VALUES (?, ?, ?, 'waiting', ?)`, runID, nodeID, nextSeq, now); err != nil {
		return fmt.Errorf("creating retry attempt: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'ready', ready_at = ?, completed_at = NULL WHERE run_id = ? AND node_id = ?`, now, runID, nodeID); err != nil {
		return fmt.Errorf("readying node for retry: %w", err)
	}
	if _, err = tx.Exec(`UPDATE workflow_run SET updated_at = ? WHERE id = ?`, now, runID); err != nil {
		return fmt.Errorf("updating workflow run: %w", err)
	}
	return tx.Commit()
}
