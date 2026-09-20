package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) decideWorkflowStep(ctx context.Context, mutation model.GraphMutation) error {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	if mutation.Actor != "user" {
		return invalid("workflow approval requires a user decision")
	}
	if _, err := d.reconcileFactoryWorkflow(ctx, mutation.EpicID); err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// A user may reconsider a rejected step, but its prerequisites are checked again.
	if mutation.Action == "approve_step" {
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '' WHERE id = ? AND epic_id = ? AND kind = 'approval' AND status = 'closed' AND outcome = 'failed'`, mutation.IssueID, mutation.EpicID); err != nil {
			return err
		}
	}
	issues, err := listFactoryIssues(ctx, tx, mutation.EpicID)
	if err != nil {
		return err
	}
	found := false
	for _, issue := range issues {
		if issue.ID == mutation.IssueID && issue.Kind == "approval" && issue.Status == "open" && issue.DispatchState == "ready" && issue.Workflow != nil {
			found = true
		}
	}
	if !found {
		return invalid("workflow approval is not ready")
	}
	outcome := "succeeded"
	if mutation.Action == "reject_step" {
		outcome = "failed"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = ? WHERE id = ?`, outcome, mutation.IssueID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record(epic_id, work_item_id, actor, action, details_json, created_at) VALUES (?, ?, 'user', ?, '{}', ?)`, mutation.EpicID, mutation.IssueID, mutation.Action, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func putWorkflowStep(ctx context.Context, tx *sql.Tx, id string, step model.WorkflowStep) error {
	raw, err := json.Marshal(step)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_workflow_step(issue_id, definition_json) VALUES (?, ?)`, id, string(raw))
	return err
}

func workflowSteps(ctx context.Context, reader factoryIssueReader, epicID string) (map[string]*model.WorkflowStep, error) {
	rows, err := reader.QueryContext(ctx, `SELECT w.issue_id, w.definition_json FROM factory_workflow_step w JOIN factory_issue i ON i.id = w.issue_id WHERE i.epic_id = ? AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := map[string]*model.WorkflowStep{}
	for rows.Next() {
		var id, raw string
		var step model.WorkflowStep
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &step); err != nil {
			return nil, fmt.Errorf("invalid pinned workflow step: %w", err)
		}
		steps[id] = &step
	}
	return steps, rows.Err()
}

func attachWorkflowSteps(ctx context.Context, reader factoryIssueReader, epicID string, issues []model.NativeIssue) error {
	steps, err := workflowSteps(ctx, reader, epicID)
	if err != nil {
		return err
	}
	parents := map[string]string{}
	for _, issue := range issues {
		parents[issue.ID] = issue.ParentID
	}
	for index := range issues {
		for id, depth := issues[index].ID, 0; id != "" && depth < len(issues); id, depth = parents[id], depth+1 {
			if step := steps[id]; step != nil {
				issues[index].Workflow = step
				break
			}
		}
	}
	return nil
}

// Reconcile group completion and expand project-scoped checks/delivery. Everything
// is persisted in one transaction, so a restart cannot open a partial barrier.
func (d *DB) reconcileFactoryWorkflow(ctx context.Context, epicID string) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	steps, err := workflowSteps(ctx, tx, epicID)
	if err != nil || len(steps) == 0 {
		return false, err
	}
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return true, err
	}
	projects := map[string]bool{}
	for _, issue := range issues {
		participating := issue.Requirement != "optional" || (issue.Status != "deferred" && issue.DispatchState != "not_applicable" && issue.DispatchState != "terminally_blocked" && (issue.Status != "closed" || issue.Outcome == "succeeded"))
		if participating && issue.Workflow != nil && issue.Workflow.Kind == "implementation" && (issue.Kind == "implementation" || issue.Kind == "task") {
			projects[issue.Project] = true
		}
		if issue.Kind != "phase" {
			continue
		}
		status, outcome := "closed", "succeeded"
		count := 0
		for _, child := range issues {
			if child.ParentID != issue.ID || child.Requirement == "reference" || child.DispatchState == "not_applicable" {
				continue
			}
			if child.Requirement == "optional" && (child.Status == "closed" || child.Status == "deferred" || child.DispatchState == "terminally_blocked") {
				continue
			}
			count++
			if child.Requirement == "required" && child.Status == "closed" && child.Outcome != "succeeded" {
				status, outcome = "closed", "failed"
				break
			}
			if child.Status != "closed" {
				status, outcome = "open", ""
			}
		}
		if count == 0 {
			status, outcome = "open", ""
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = ?, outcome = ? WHERE id = ?`, status, outcome, issue.ID); err != nil {
			return true, err
		}
	}
	paths := make([]string, 0, len(projects))
	for path := range projects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	// Optional work that never runs must not leave a mandatory PR for an unchanged project.
	for _, issue := range issues {
		step := steps[issue.ID]
		if step == nil || (issue.Kind != "task" && issue.Kind != "delivery") || (step.Kind != "verification" && step.Kind != "delivery") || projects[issue.Project] || issue.Status == "in_progress" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = 'Project has no participating work' WHERE id = ?`, issue.ID); err != nil {
			return true, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO factory_removed_issue(issue_id, plan_id, plan_revision, removed_at) VALUES (?, ?, 0, ?)`, issue.ID, epicID, time.Now().UnixMilli()); err != nil {
			return true, err
		}
		delete(steps, issue.ID)
	}
	// Each verification/delivery step runs once per changed project. All instances
	// of a prerequisite must finish before its dependent step can run.
	for _, template := range issues {
		step := steps[template.ID]
		if step == nil || (step.Kind != "verification" && step.Kind != "delivery") {
			continue
		}
		for index, path := range paths {
			found := false
			for _, existing := range issues {
				if existing.Kind != "workflow_template" && existing.ParentID == template.ParentID && existing.Project == path && steps[existing.ID] != nil && steps[existing.ID].Key == step.Key {
					found = true
					break
				}
			}
			if found {
				continue
			}
			kind := "task"
			if step.Kind == "delivery" {
				kind = "delivery"
			}
			id := template.ID
			if template.Kind == "workflow_template" && index == 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET kind = ?, project_path = ? WHERE id = ?`, kind, path, id); err != nil {
					return true, err
				}
			} else {
				id, err = factoryChildID(ctx, tx, template.ParentID)
				if err != nil {
					return true, err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue(id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, ?, ?, ?, 'open', ?)`, id, epicID, path, kind, template.Title, step.Prompt, time.Now().UnixMilli()); err != nil {
					return true, err
				}
				childIndex, err := factoryChildIndex(id)
				if err != nil {
					return true, err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy(parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, template.ParentID, id, childIndex); err != nil {
					return true, err
				}
				if err := putWorkflowStep(ctx, tx, id, *step); err != nil {
					return true, err
				}
			}
			created := template
			created.ID, created.Kind, created.Project = id, kind, path
			issues = append(issues, created)
			steps[id] = step
		}
	}
	for _, issue := range issues {
		step := steps[issue.ID]
		if step == nil {
			continue
		}
		for _, need := range step.Needs {
			for _, blocker := range issues {
				if blocker.ParentID == issue.ParentID && steps[blocker.ID] != nil && steps[blocker.ID].Key == need {
					if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO factory_issue_dependency(issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks')`, issue.ID, blocker.ID); err != nil {
						return true, err
					}
				}
			}
		}
	}
	// Reopening implementation invalidates successful downstream checks/approvals.
	for range len(steps) {
		result, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '' WHERE epic_id = ? AND status = 'closed' AND outcome = 'succeeded' AND kind IN ('task', 'approval', 'delivery') AND id IN (SELECT issue_id FROM factory_workflow_step) AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id) AND EXISTS (SELECT 1 FROM factory_issue_dependency d JOIN factory_issue b ON b.id = d.depends_on_issue_id WHERE d.issue_id = factory_issue.id AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = b.id) AND (b.status <> 'closed' OR b.outcome <> 'succeeded'))`, epicID)
		if err != nil {
			return true, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return true, err
		}
		if changed == 0 {
			break
		}
	}
	return true, tx.Commit()
}
