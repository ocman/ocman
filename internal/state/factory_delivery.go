package state

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) FactoryAttemptHasRecoveryResponse(ctx context.Context, attemptID, response string) (bool, error) {
	var found int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_recovery_gate WHERE attempt_id = ? AND resolution = 'resume' AND response = ? LIMIT 1`, attemptID, response).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Recheck the live graph under the claim transaction. Cached delivery edges are
// for display, not authority to skip work committed after their last refresh.
func validateFactoryDeliveryOrder(ctx context.Context, tx *sql.Tx, epicID, kind string) error {
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return err
	}
	byID := make(map[string]*model.NativeIssue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}
	for _, issue := range issues {
		if issue.Kind == "delivery" && issue.Status == "closed" && issue.Outcome == "succeeded" {
			return errors.New("factory final delivery is already complete")
		}
		if kind != "delivery" || issue.Kind == "delivery" || issue.Kind == "mol" || issue.DispatchState == "not_applicable" || (issue.Kind == "gate" && issue.GateResolution == "") {
			continue
		}
		requirement := factoryIssueRequirement(&issue, byID)
		if requirement == "reference" {
			continue
		}
		succeeded := issue.Status == "closed" && issue.Outcome == "succeeded" && (issue.Kind != "gate" || issue.GateResolution == "approved")
		if requirement == "required" && !succeeded {
			return errors.New("factory delivery requires all required work to succeed")
		}
		if (issue.Kind == "task" || issue.Kind == "implementation") && (issue.DispatchState == "ready" || issue.Status == "in_progress") {
			return errors.New("factory delivery must wait for runnable optional work")
		}
	}
	return nil
}

// EnsureFactoryDeliveryIssue gives delivery its own retryable issue. Dependencies
// follow the current required work, including tasks added after materialization.
func (d *DB) EnsureFactoryDeliveryIssue(ctx context.Context, epicID string) error {
	issues, err := d.ListFactoryIssues(ctx, epicID)
	if err != nil {
		return err
	}
	var delivery, parent string
	var blockers []string
	executable := false
	byID := make(map[string]*model.NativeIssue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}
	for _, issue := range issues {
		if issue.Kind == "delivery" {
			delivery = issue.ID
			continue
		}
		if factoryIssueRequirement(&issue, byID) != "required" || issue.DispatchState == "not_applicable" || issue.Kind == "mol" || (issue.Kind == "gate" && issue.GateResolution == "") {
			continue
		}
		if issue.Kind == "implementation" || issue.Kind == "task" {
			parent = issue.ParentID
			executable = true
		}
		blockers = append(blockers, issue.ID)
	}
	if !executable {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Recheck under the write transaction; dispatch may run concurrently.
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), '') FROM factory_issue WHERE epic_id = ? AND kind = 'delivery' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, epicID).Scan(&delivery); err != nil {
		return err
	}
	if delivery == "" {
		delivery, err = factoryChildID(ctx, tx, parent)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) VALUES (?, ?, 'delivery', 'Deliver the completed work', 'Review the combined changes, run the required checks, and create or reuse the final pull request.', 'open', ?)`, delivery, epicID, time.Now().UnixMilli()); err != nil {
			return err
		}
		index, err := factoryChildIndex(delivery)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, parent, delivery, index); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM factory_issue_dependency WHERE issue_id = ?`, delivery); err != nil {
		return err
	}
	for _, blocker := range blockers {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks') ON CONFLICT DO NOTHING`, delivery, blocker); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetFactoryAttemptWorkspace freezes the branch and accepted checkpoint before launch.
func (d *DB) SetFactoryAttemptWorkspace(ctx context.Context, id string, policy model.FactoryAttemptPolicy) error {
	if policy.Branch == "" || policy.TargetBranch == "" {
		return errors.New("factory workspace requires branch and target")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE factory_attempt SET frozen_policy_json = json_set(frozen_policy_json, '$.branch', ?, '$.baseRef', ?, '$.targetBranch', ?, '$.checkpointSha', ?, '$.delivery', json(?)) WHERE id = ? AND phase = 'prepared'`, policy.Branch, policy.BaseRef, policy.TargetBranch, policy.CheckpointSHA, strconv.FormatBool(policy.Delivery), id)
	changed, err := factoryAttemptChanged(result, err, "recording Factory workspace")
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("factory attempt is not prepared")
	}
	return nil
}
