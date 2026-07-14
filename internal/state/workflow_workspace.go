package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Workspace lease modes mirror the workflows package constants. Kept as
// plain strings here so the state layer has no dependency on workflows.
const (
	workspaceExclusive = "exclusive"
	workspacePath      = "path"
)

// WorkflowWorkspaceLease is a durable record of a shard owned by one node
// attempt. Mode is "exclusive" or "path". Paths are normalized scope
// strings (empty for exclusive). CommitLease marks a serialized-commit
// coordinator. Host is an optional owning-host identity; the first
// scheduler always uses the local host.
type WorkflowWorkspaceLease struct {
	RunID       string
	NodeID      string
	AttemptID   int64
	Shard       int
	Mode        string
	Paths       []string
	CommitLease bool
	Host        string
	ShardPath   string
	AcquiredAt  int64
}

// WorkflowWorkspaceRequest is a node's demand for a workspace lease within
// a run's bounded shard pool. Shards is the pool size. When CommitLease is
// set the request needs serialized per-shard commit capacity: it may share
// a shard with path leases but not with another commit or exclusive lease.
type WorkflowWorkspaceRequest struct {
	Shards      int
	Mode        string
	Paths       []string
	CommitLease bool
	Host        string
}

// acquireWorkflowWorkspaceLease atomically assigns the node a shard within
// tx, honoring exclusive/path/commit sharing rules. Idempotent per (run,
// node): a re-acquire keeps the existing lease. Returns (shard, true) on
// success, (0, false) without error when no shard can host the request
// (shard exhaustion or unavoidable overlap).
//
// Sharing rules per shard:
//   - exclusive lease: shard must be empty.
//   - path lease: shard may hold other path leases only when no declared
//     scope overlaps; it may not share with an exclusive lease.
//   - commit lease (coordinator): shard may hold path leases but not
//     another commit lease or an exclusive lease (serialized commits).
func acquireWorkflowWorkspaceLease(tx *sql.Tx, runID, nodeID string, attemptID int64, req WorkflowWorkspaceRequest, now int64) (int, bool, error) {
	if req.Shards <= 0 {
		return 0, false, fmt.Errorf("workspace shard pool must be positive")
	}
	// Idempotent: reuse an existing lease for this node.
	var existing int
	err := tx.QueryRow(`SELECT shard FROM workflow_workspace_lease WHERE run_id = ? AND node_id = ?`, runID, nodeID).Scan(&existing)
	if err == nil {
		return existing, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, fmt.Errorf("reading existing workspace lease: %w", err)
	}

	occupants, err := shardOccupants(tx, runID)
	if err != nil {
		return 0, false, err
	}
	mode := req.Mode
	if mode == "" {
		mode = workspaceExclusive
	}
	pathsJSON, err := json.Marshal(req.Paths)
	if err != nil {
		return 0, false, fmt.Errorf("encoding lease paths: %w", err)
	}
	for shard := 0; shard < req.Shards; shard++ {
		if !shardAdmits(occupants[shard], mode, req) {
			continue
		}
		commit := 0
		if req.CommitLease {
			commit = 1
		}
		if _, err := tx.Exec(`INSERT INTO workflow_workspace_lease (run_id, node_id, attempt_id, shard, mode, paths_json, commit_lease, host, shard_path, acquired_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
			runID, nodeID, attemptID, shard, mode, string(pathsJSON), commit, req.Host, now); err != nil {
			return 0, false, fmt.Errorf("holding workspace lease: %w", err)
		}
		return shard, true, nil
	}
	return 0, false, nil
}

// shardAdmits reports whether a shard's current occupants can accept a new
// lease of the given mode/request.
func shardAdmits(occupants []WorkflowWorkspaceLease, mode string, req WorkflowWorkspaceRequest) bool {
	for _, occ := range occupants {
		// Any existing exclusive lease blocks everything.
		if occ.Mode == workspaceExclusive {
			return false
		}
		switch {
		case mode == workspaceExclusive:
			// A new exclusive lease needs an empty shard.
			return false
		case req.CommitLease && occ.CommitLease:
			// Commits are serialized per shard: at most one coordinator.
			return false
		case mode == workspacePath && !req.CommitLease && occ.CommitLease:
			// A plain path lease may coexist with a coordinator only if
			// scopes are disjoint (checked below), so fall through.
			if pathSetsConflict(req.Paths, occ.Paths) {
				return false
			}
		case mode == workspacePath && occ.Mode == workspacePath:
			if pathSetsConflict(req.Paths, occ.Paths) {
				return false
			}
		}
	}
	return true
}

// pathSetsConflict reports whether any normalized scope in a overlaps any
// in b. Exact match or an ancestor/descendant relationship is a conflict;
// disjoint siblings are not. Empty scope sets (a coordinator with no
// declared paths) conflict with everything, matching exclusive-commit
// semantics.
func pathSetsConflict(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if x == y || isPathAncestor(x, y) || isPathAncestor(y, x) {
				return true
			}
		}
	}
	return false
}

func isPathAncestor(ancestor, descendant string) bool {
	return len(descendant) > len(ancestor)+1 &&
		descendant[:len(ancestor)] == ancestor &&
		descendant[len(ancestor)] == '/'
}

// shardOccupants returns the current leases grouped by shard index.
func shardOccupants(tx *sql.Tx, runID string) (map[int][]WorkflowWorkspaceLease, error) {
	rows, err := tx.Query(`SELECT node_id, attempt_id, shard, mode, paths_json, commit_lease, host FROM workflow_workspace_lease WHERE run_id = ?`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing shard occupants: %w", err)
	}
	defer rows.Close()
	out := map[int][]WorkflowWorkspaceLease{}
	for rows.Next() {
		var lease WorkflowWorkspaceLease
		var pathsJSON string
		var commit int
		if err := rows.Scan(&lease.NodeID, &lease.AttemptID, &lease.Shard, &lease.Mode, &pathsJSON, &commit, &lease.Host); err != nil {
			return nil, fmt.Errorf("scanning shard occupant: %w", err)
		}
		lease.CommitLease = commit != 0
		if err := json.Unmarshal([]byte(pathsJSON), &lease.Paths); err != nil {
			return nil, fmt.Errorf("decoding lease paths: %w", err)
		}
		out[lease.Shard] = append(out[lease.Shard], lease)
	}
	return out, rows.Err()
}

// releaseWorkflowWorkspaceLease drops a node's shard lease within tx.
func releaseWorkflowWorkspaceLease(exec workflowRunExecer, runID, nodeID string) error {
	if _, err := exec.Exec(`DELETE FROM workflow_workspace_lease WHERE run_id = ? AND node_id = ?`, runID, nodeID); err != nil {
		return fmt.Errorf("releasing workspace lease: %w", err)
	}
	return nil
}

// SetWorkflowWorkspaceShardPath records the on-disk path of a shard once
// the worktree is created, so the UI and later nodes can resolve it.
func (d *DB) SetWorkflowWorkspaceShardPath(runID, nodeID, shardPath string) error {
	_, err := d.db.Exec(`UPDATE workflow_workspace_lease SET shard_path = ? WHERE run_id = ? AND node_id = ?`, shardPath, runID, nodeID)
	if err != nil {
		return fmt.Errorf("recording shard path: %w", err)
	}
	return nil
}

// ListWorkflowWorkspaceLeases returns workspace leases held for a run, for
// UI and restart reconciliation.
func (d *DB) ListWorkflowWorkspaceLeases(runID string) ([]WorkflowWorkspaceLease, error) {
	rows, err := d.db.Query(`SELECT run_id, node_id, attempt_id, shard, mode, paths_json, commit_lease, host, shard_path, acquired_at FROM workflow_workspace_lease WHERE run_id = ? ORDER BY shard, node_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing workspace leases: %w", err)
	}
	defer rows.Close()
	var out []WorkflowWorkspaceLease
	for rows.Next() {
		var lease WorkflowWorkspaceLease
		var pathsJSON string
		var commit int
		if err := rows.Scan(&lease.RunID, &lease.NodeID, &lease.AttemptID, &lease.Shard, &lease.Mode, &pathsJSON, &commit, &lease.Host, &lease.ShardPath, &lease.AcquiredAt); err != nil {
			return nil, fmt.Errorf("scanning workspace lease: %w", err)
		}
		lease.CommitLease = commit != 0
		if err := json.Unmarshal([]byte(pathsJSON), &lease.Paths); err != nil {
			return nil, fmt.Errorf("decoding lease paths: %w", err)
		}
		out = append(out, lease)
	}
	return out, rows.Err()
}
