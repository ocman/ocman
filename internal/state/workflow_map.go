package state

import (
	"context"
	"errors"
	"fmt"
)

// WorkflowMapItem is one durable mapped item: its stable key, input
// order, the child run executing its per-item subworkflow, and its
// terminal state. The stable key is the idempotency anchor so restart or
// retry never reprocesses a completed item.
type WorkflowMapItem struct {
	RunID      string
	MapNode    string
	ItemKey    string
	ItemIndex  int
	ChildRunID string
	State      string
	CreatedAt  int64
}

// CreateWorkflowMapItem inserts a mapped item together with its executing
// child run in one transaction. The insert is idempotent on the stable
// key: if the item already exists (a restart re-expanding the map), it is
// left untouched and (false, nil) is returned so the caller does not
// relaunch completed work.
func (d *DB) CreateWorkflowMapItem(ctx context.Context, item WorkflowMapItem, child WorkflowRun) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning map item creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_map_item WHERE run_id = ? AND map_node = ? AND item_key = ?`, item.RunID, item.MapNode, item.ItemKey).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking existing map item: %w", err)
	}
	if exists > 0 {
		return false, nil
	}
	if err := insertWorkflowRun(ctx, tx, child); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_map_item (run_id, map_node, item_key, item_index, child_run_id, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.RunID, item.MapNode, item.ItemKey, item.ItemIndex, nullableString(item.ChildRunID), item.State, item.CreatedAt); err != nil {
		return false, fmt.Errorf("inserting map item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing map item creation: %w", err)
	}
	return true, nil
}

// ListWorkflowMapItems returns a map node's items in input order.
func (d *DB) ListWorkflowMapItems(ctx context.Context, runID, mapNode string) ([]WorkflowMapItem, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT run_id, map_node, item_key, item_index, COALESCE(child_run_id, ''), state, created_at FROM workflow_map_item WHERE run_id = ? AND map_node = ? ORDER BY item_index`, runID, mapNode)
	if err != nil {
		return nil, fmt.Errorf("listing map items: %w", err)
	}
	defer rows.Close()
	var out []WorkflowMapItem
	for rows.Next() {
		var item WorkflowMapItem
		if err := rows.Scan(&item.RunID, &item.MapNode, &item.ItemKey, &item.ItemIndex, &item.ChildRunID, &item.State, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning map item: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SetWorkflowMapItemState records an item's terminal state so restart
// recovery and the join can read it without re-inspecting the child run.
func (d *DB) SetWorkflowMapItemState(ctx context.Context, runID, mapNode, itemKey, state string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE workflow_map_item SET state = ? WHERE run_id = ? AND map_node = ? AND item_key = ?`, state, runID, mapNode, itemKey)
	if err != nil {
		return fmt.Errorf("updating map item state: %w", err)
	}
	return nil
}

// StartWorkflowNode flips a node's waiting attempt to running, holding it
// while the service drives a map's child runs. Returns false if the
// attempt is not waiting (already started / lost race) or the run is not
// active. Reuses the resource-lease machinery so a map consumes the parent
// run's concurrency scope while its items execute.
func (d *DB) StartWorkflowNode(ctx context.Context, runID, nodeID string, requests []WorkflowResourceRequest, now int64) (bool, error) {
	return d.StartWorkflowCommand(ctx, runID, nodeID, requests, nil, now)
}

// SettleWorkflowNode records a terminal outcome for a running/ready map or
// join node: it completes the node + its latest non-terminal attempt with
// the collected outputs, releases held resources, and cascades readiness or
// unreachable-branch skips, mirroring the command/agent completion cascade.
func (d *DB) SettleWorkflowNode(ctx context.Context, runID, nodeID string, attemptID int64, successful bool, outputsJSON, attemptError string, now int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning node settle: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var runState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM workflow_run WHERE id = ?`, runID).Scan(&runState); err != nil {
		return fmt.Errorf("getting workflow run for settle: %w", err)
	}
	if runState != "active" && runState != "paused" {
		return fmt.Errorf("workflow run is not active or paused")
	}
	nodeState, attemptState := "successful", "successful"
	if !successful {
		nodeState, attemptState = "failed", "failed"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state IN ('ready', 'running')`, nodeState, now, runID, nodeID); err != nil {
		return fmt.Errorf("settling workflow node: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE workflow_node_attempt SET state = ?, outputs_json = ?, error = ?, completed_at = ? WHERE id = ? AND state IN ('waiting', 'running')`, attemptState, outputsJSON, attemptError, now, attemptID)
	if err != nil {
		return fmt.Errorf("settling workflow node attempt: %w", err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return errors.New("workflow node attempt is not settleable")
	}
	if err = releaseWorkflowResources(ctx, tx, runID, nodeID); err != nil {
		return err
	}
	if !successful {
		return completeWorkflowNodeTx(ctx, tx, runID, now)
	}
	return completeWorkflowNodeTx(ctx, tx, runID, now)
}

// SkipWorkflowNode records a condition that evaluated false or errored before
// the node starts. It is deliberately terminal and creates no executor work.
func (d *DB) SkipWorkflowNode(ctx context.Context, runID, nodeID, reason string, now int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning node skip: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'ready'`, now, runID, nodeID); err != nil {
		return fmt.Errorf("skipping workflow node: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_attempt SET state = 'skipped', error = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'waiting'`, reason, now, runID, nodeID); err != nil {
		return fmt.Errorf("skipping workflow attempt: %w", err)
	}
	return completeWorkflowNodeTx(ctx, tx, runID, now)
}

// RepeatWorkflowNode turns a successful node back into ready with a distinct
// attempt. Direct dependents are returned to pending before they can start.
func (d *DB) RepeatWorkflowNode(ctx context.Context, runID, nodeID string, attemptID int64, reason string, now int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning workflow repeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var versionID string
	var seq int
	if err = tx.QueryRowContext(ctx, `SELECT version_id FROM workflow_run WHERE id = ? AND state IN ('active', 'successful')`, runID).Scan(&versionID); err != nil {
		return fmt.Errorf("getting repeatable workflow run: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_run SET state = 'active', completed_at = NULL, updated_at = ? WHERE id = ?`, now, runID); err != nil {
		return fmt.Errorf("reactivating workflow repeat: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_run SET state = 'ready', completed_at = NULL, ready_at = ? WHERE run_id = ? AND node_id = ? AND state = 'successful'`, now, runID, nodeID); err != nil {
		return fmt.Errorf("resetting repeated node: %w", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_node_attempt WHERE run_id = ? AND node_id = ?`, runID, nodeID).Scan(&seq); err != nil {
		return fmt.Errorf("numbering repeat attempt: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at, error) VALUES (?, ?, ?, 'waiting', ?, ?)`, runID, nodeID, seq, now, reason); err != nil {
		return fmt.Errorf("inserting repeat attempt: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_run SET state = 'pending', ready_at = NULL WHERE run_id = ? AND state = 'ready' AND node_id IN (SELECT to_node FROM workflow_version_dependency WHERE version_id = ? AND from_node = ?)`, runID, versionID, nodeID); err != nil {
		return fmt.Errorf("blocking repeat dependents: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_attempt SET state = 'canceled', completed_at = ? WHERE run_id = ? AND state = 'waiting' AND node_id IN (SELECT to_node FROM workflow_version_dependency WHERE version_id = ? AND from_node = ?)`, now, runID, versionID, nodeID); err != nil {
		return fmt.Errorf("canceling premature repeat dependents: %w", err)
	}
	return tx.Commit()
}

// ExhaustWorkflowRepeat makes an unmet bounded repeat policy visible as a
// failed terminal node instead of silently succeeding its final body attempt.
func (d *DB) ExhaustWorkflowRepeat(ctx context.Context, runID, nodeID, reason string, now int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning repeat exhaustion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_run SET state = 'failed', completed_at = ? WHERE run_id = ? AND node_id = ? AND state = 'successful'`, now, runID, nodeID); err != nil {
		return fmt.Errorf("failing exhausted repeat node: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_node_attempt SET error = ? WHERE id = (SELECT MAX(id) FROM workflow_node_attempt WHERE run_id = ? AND node_id = ?)`, reason, runID, nodeID); err != nil {
		return fmt.Errorf("recording repeat exhaustion: %w", err)
	}
	return completeWorkflowNodeTx(ctx, tx, runID, now)
}
