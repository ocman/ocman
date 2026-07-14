package state

import (
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
func (d *DB) CreateWorkflowMapItem(item WorkflowMapItem, child WorkflowRun) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, fmt.Errorf("beginning map item creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM workflow_map_item WHERE run_id = ? AND map_node = ? AND item_key = ?`, item.RunID, item.MapNode, item.ItemKey).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking existing map item: %w", err)
	}
	if exists > 0 {
		return false, nil
	}
	if err := insertWorkflowRun(tx, child); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO workflow_map_item (run_id, map_node, item_key, item_index, child_run_id, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.RunID, item.MapNode, item.ItemKey, item.ItemIndex, nullableString(item.ChildRunID), item.State, item.CreatedAt); err != nil {
		return false, fmt.Errorf("inserting map item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing map item creation: %w", err)
	}
	return true, nil
}

// ListWorkflowMapItems returns a map node's items in input order.
func (d *DB) ListWorkflowMapItems(runID, mapNode string) ([]WorkflowMapItem, error) {
	rows, err := d.db.Query(`SELECT run_id, map_node, item_key, item_index, COALESCE(child_run_id, ''), state, created_at FROM workflow_map_item WHERE run_id = ? AND map_node = ? ORDER BY item_index`, runID, mapNode)
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
func (d *DB) SetWorkflowMapItemState(runID, mapNode, itemKey, state string) error {
	_, err := d.db.Exec(`UPDATE workflow_map_item SET state = ? WHERE run_id = ? AND map_node = ? AND item_key = ?`, state, runID, mapNode, itemKey)
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
func (d *DB) StartWorkflowNode(runID, nodeID string, requests []WorkflowResourceRequest, now int64) (bool, error) {
	return d.StartWorkflowCommand(runID, nodeID, requests, nil, now)
}

// SettleWorkflowNode records a terminal outcome for a running/ready map or
// join node: it completes the node + its latest non-terminal attempt with
// the collected outputs, releases held resources, and either cascades
// readiness to dependents (success) or fails the run (failure), mirroring
// the command/agent completion cascade.
func (d *DB) SettleWorkflowNode(runID, nodeID string, attemptID int64, successful bool, outputsJSON, attemptError string, now int64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning node settle: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var runState string
	if err = tx.QueryRow(`SELECT state FROM workflow_run WHERE id = ?`, runID).Scan(&runState); err != nil {
		return fmt.Errorf("getting workflow run for settle: %w", err)
	}
	if runState != "active" && runState != "paused" {
		return fmt.Errorf("workflow run is not active or paused")
	}
	nodeState, attemptState := "successful", "successful"
	if !successful {
		nodeState, attemptState = "failed", "failed"
	}
	if _, err = tx.Exec(`UPDATE workflow_node_run SET state = ?, completed_at = ? WHERE run_id = ? AND node_id = ? AND state IN ('ready', 'running')`, nodeState, now, runID, nodeID); err != nil {
		return fmt.Errorf("settling workflow node: %w", err)
	}
	res, err := tx.Exec(`UPDATE workflow_node_attempt SET state = ?, outputs_json = ?, error = ?, completed_at = ? WHERE id = ? AND state IN ('waiting', 'running')`, attemptState, outputsJSON, attemptError, now, attemptID)
	if err != nil {
		return fmt.Errorf("settling workflow node attempt: %w", err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return errors.New("workflow node attempt is not settleable")
	}
	if err = releaseWorkflowResources(tx, runID, nodeID); err != nil {
		return err
	}
	if !successful {
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'skipped', completed_at = ? WHERE run_id = ? AND state = 'pending'`, now, runID); err != nil {
			return fmt.Errorf("skipping settle descendants: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_run SET state = 'canceled', completed_at = ? WHERE run_id = ? AND node_id != ? AND state IN ('ready', 'running')`, now, runID, nodeID); err != nil {
			return fmt.Errorf("canceling sibling nodes on settle: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_node_attempt SET state = 'canceled', completed_at = ? WHERE run_id = ? AND node_id != ? AND state IN ('waiting', 'running', 'starting')`, now, runID, nodeID); err != nil {
			return fmt.Errorf("canceling sibling attempts on settle: %w", err)
		}
		if _, err = tx.Exec(`DELETE FROM workflow_resource_lease WHERE run_id = ?`, runID); err != nil {
			return fmt.Errorf("releasing resources on settle failure: %w", err)
		}
		if _, err = tx.Exec(`UPDATE workflow_run SET state = 'failed', updated_at = ?, completed_at = ? WHERE id = ?`, now, now, runID); err != nil {
			return fmt.Errorf("failing workflow run on settle: %w", err)
		}
		return tx.Commit()
	}
	return completeWorkflowNodeTx(tx, runID, now)
}
