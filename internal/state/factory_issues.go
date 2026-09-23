package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) ListFactoryIssues(ctx context.Context, epicID string) ([]model.NativeIssue, error) {
	if _, err := d.GetFactoryEpic(ctx, epicID); err != nil {
		return nil, err
	}
	return listFactoryIssues(ctx, d.db, epicID)
}

type factoryIssueReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// The claim path uses the same dispatch calculation inside its transaction.
func listFactoryIssues(ctx context.Context, reader factoryIssueReader, epicID string) ([]model.NativeIssue, error) {
	rows, err := reader.QueryContext(ctx, `SELECT i.id, i.epic_id, i.project_path, COALESCE(h.parent_issue_id, ''), COALESCE(h.requirement, ''), i.kind, i.title, i.status, i.description, COALESCE(f.formula_id, ''), COALESCE(f.formula_version, 0), COALESCE(f.formula_hash, ''), COALESCE(f.bindings_json, '{}'), COALESCE(m.proposal_revision, 0), COALESCE(p.manifest_key, ''), i.outcome, i.outcome_reason, COALESCE(g.resolution, pg.resolution, ''), i.retry_at, i.retry_attempts, i.created_at, COALESCE(pg.attempt_id, ''), COALESCE(pg.work_item_id, ''), COALESCE(pg.requested_project, ''), COALESCE(pg.canonical_project, ''), COALESCE(pg.reason, ''), COALESCE(pg.response, ''), COALESCE(pg.resolution, ''), COALESCE(pg.plan_issue_id, '') FROM factory_issue i LEFT JOIN factory_issue_hierarchy h ON h.child_issue_id = i.id LEFT JOIN factory_mol_formula f ON f.mol_id = i.id LEFT JOIN factory_materialization_provenance p ON p.entity_kind = 'issue' AND p.entity_id = i.id LEFT JOIN factory_materialization m ON m.id = p.materialization_id LEFT JOIN factory_plan_gate g ON g.issue_id = i.id LEFT JOIN factory_project_request_gate pg ON pg.issue_id = i.id LEFT JOIN factory_removed_issue r ON r.issue_id = i.id WHERE i.epic_id = ? AND r.issue_id IS NULL ORDER BY CASE i.kind WHEN 'mol' THEN 0 WHEN 'plan' THEN 1 WHEN 'gate' THEN 2 ELSE 3 END`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing Factory issues: %w", err)
	}
	defer rows.Close()
	var issues []model.NativeIssue
	for rows.Next() {
		var issue model.NativeIssue
		var bindings string
		var projectGate model.ProjectRequestGate
		if err := rows.Scan(&issue.ID, &issue.EpicID, &issue.Project, &issue.ParentID, &issue.Requirement, &issue.Kind, &issue.Title, &issue.Status, &issue.Description, &issue.FormulaID, &issue.FormulaVersion, &issue.FormulaHash, &bindings, &issue.PlanRevision, &issue.ManifestKey, &issue.Outcome, &issue.OutcomeReason, &issue.GateResolution, &issue.RetryAt, &issue.RetryAttempts, &issue.CreatedAt, &projectGate.AttemptID, &projectGate.WorkID, &projectGate.RequestedProject, &projectGate.CanonicalProject, &projectGate.Reason, &projectGate.Response, &projectGate.Resolution, &projectGate.PlanIssueID); err != nil {
			return nil, fmt.Errorf("scanning Factory issue: %w", err)
		}
		if projectGate.AttemptID != "" {
			projectGate.IssueID, projectGate.EpicID = issue.ID, issue.EpicID
			issue.ProjectRequestGate = &projectGate
		}
		if err := json.Unmarshal([]byte(bindings), &issue.Bindings); err != nil {
			return nil, fmt.Errorf("decoding Factory Mol bindings: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := attachWorkflowSteps(ctx, reader, epicID, issues); err != nil {
		return nil, err
	}
	return deriveFactoryIssueDispatch(ctx, reader, epicID, issues)
}

// ListRemovedFactoryIssues preserves the details of soft-deleted work for audit.
func (d *DB) ListRemovedFactoryIssues(ctx context.Context, epicID string) ([]model.NativeIssue, error) {
	if _, err := d.GetFactoryEpic(ctx, epicID); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT i.id, i.epic_id, i.project_path, i.kind, i.title, i.description, i.status, i.outcome, i.outcome_reason, r.removed_at FROM factory_removed_issue r JOIN factory_issue i ON i.id = r.issue_id WHERE i.epic_id = ? ORDER BY r.removed_at DESC, i.id`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing removed Factory issues: %w", err)
	}
	defer rows.Close()
	var issues []model.NativeIssue
	for rows.Next() {
		var issue model.NativeIssue
		if err := rows.Scan(&issue.ID, &issue.EpicID, &issue.Project, &issue.Kind, &issue.Title, &issue.Description, &issue.Status, &issue.Outcome, &issue.OutcomeReason, &issue.RemovedAt); err != nil {
			return nil, fmt.Errorf("scanning removed Factory issue: %w", err)
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func deriveFactoryIssueDispatch(ctx context.Context, reader factoryIssueReader, epicID string, issues []model.NativeIssue) ([]model.NativeIssue, error) {
	byID := make(map[string]*model.NativeIssue, len(issues))
	delivered := map[string]bool{}
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
		issues[i].DispatchState = "waiting"
	}
	rows, err := reader.QueryContext(ctx, `SELECT d.issue_id, d.type, b.id, b.epic_id, b.kind, b.status, b.outcome, b.outcome_reason, COALESCE(g.resolution, ''), COALESCE(o.status, ''), COALESCE(o.reason, '') FROM factory_issue_dependency d JOIN factory_issue b ON b.id = d.depends_on_issue_id LEFT JOIN factory_plan_gate g ON g.issue_id = b.id LEFT JOIN factory_merge_gate_observation o ON o.delivery_issue_id = b.id WHERE d.issue_id IN (SELECT id FROM factory_issue WHERE epic_id = ?) AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = b.id) ORDER BY d.issue_id, d.depends_on_issue_id`, epicID)
	if err != nil {
		return nil, err
	}
	notApplicable := map[string]bool{}
	waiting := map[string]bool{}
	blocked := map[string]bool{}
	for rows.Next() {
		var issueID, edgeType, blockerID, blockerEpicID, kind, status, outcome, reason, resolution, mergeStatus, mergeReason string
		if err := rows.Scan(&issueID, &edgeType, &blockerID, &blockerEpicID, &kind, &status, &outcome, &reason, &resolution, &mergeStatus, &mergeReason); err != nil {
			rows.Close()
			return nil, err
		}
		issue := byID[issueID]
		if issue == nil {
			continue
		}
		// Every declared edge is reported so the graph keeps its shape; Blockers below
		// stay restricted to the unsatisfied edges that actually hold work back.
		issue.DependsOn = append(issue.DependsOn, model.NativeIssueDependency{ID: blockerID, Type: edgeType})
		if issue.Status != "open" || factoryIssueRequirement(issue, byID) == "reference" {
			continue
		}
		succeeded := status == "closed" && outcome == "succeeded" && (kind != "gate" || resolution == "approved")
		failed := status == "closed" && ((kind != "gate" && outcome == "failed") || (kind == "gate" && resolution == "rejected"))
		switch edgeType {
		case "blocks":
			if succeeded {
				continue
			}
			if status == "closed" {
				blocked[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Type: edgeType, Reason: reason, Outcome: outcome})
			} else {
				waiting[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Type: edgeType, Reason: reason, Outcome: outcome})
			}
		case "merge_gated":
			if mergeStatus == "merged" {
				continue
			}
			if mergeReason == "" {
				switch mergeStatus {
				case "closed":
					mergeReason = "Project Delivery PR was closed without merge. Reopen or replace the PR."
				case "open":
					mergeReason = "Waiting for the Project Delivery PR to merge."
				case "draft":
					mergeReason = "Waiting for the draft Project Delivery PR to become ready and merge."
				default:
					mergeReason = "Waiting for the forge to verify the Project Delivery PR."
				}
			}
			issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Type: edgeType, Reason: mergeReason, Outcome: mergeStatus})
			if mergeStatus == "closed" || mergeStatus == "changed" {
				blocked[issueID] = true
			} else {
				waiting[issueID] = true
			}
		default: // on_failure
			if failed {
				continue
			}
			if status == "closed" {
				notApplicable[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Type: edgeType, Reason: reason, Outcome: outcome})
			} else {
				waiting[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Type: edgeType, Reason: reason, Outcome: outcome})
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	latestDelivery := map[string]*model.NativeIssue{}
	for i := range issues {
		issue := &issues[i]
		latest := latestDelivery[issue.Project]
		if issue.Kind == "delivery" && (latest == nil || issue.CreatedAt > latest.CreatedAt || (issue.CreatedAt == latest.CreatedAt && issue.ID > latest.ID)) {
			latestDelivery[issue.Project] = issue
		}
	}
	for project, delivery := range latestDelivery {
		if delivery.Status != "closed" || delivery.Outcome != "succeeded" {
			continue
		}
		covered := map[string]bool{}
		for _, dependency := range delivery.DependsOn {
			if dependency.Type == "blocks" {
				covered[dependency.ID] = true
			}
		}
		delivered[project] = true
		for i := range issues {
			issue := &issues[i]
			if issue.Project == project && (issue.Kind == "implementation" || issue.Kind == "task") && factoryIssueRequirement(issue, byID) == "required" && !covered[issue.ID] {
				delivered[project] = false
				break
			}
		}
	}
	for i := range issues {
		issue := &issues[i]
		if factoryIssueRequirement(issue, byID) == "reference" {
			issue.DispatchState = "reference"
			continue
		}
		if delivered[issue.Project] && (issue.Kind == "task" || issue.Kind == "implementation") && issue.Status != "closed" {
			issue.DispatchState = "not_applicable"
			issue.OutcomeReason = "Final delivery is complete; this work will not run."
			continue
		}
		switch issue.Status {
		case "deferred", "retry_wait":
			issue.DispatchState = issue.Status
		case "open":
			switch {
			case notApplicable[issue.ID]:
				issue.DispatchState = "not_applicable"
			case blocked[issue.ID]:
				issue.DispatchState = "terminally_blocked"
			case waiting[issue.ID]:
				issue.DispatchState = "waiting"
			default:
				issue.DispatchState = "ready"
			}
		case "closed":
			issue.DispatchState = "completed"
		default:
			issue.DispatchState = "waiting"
		}
	}
	return issues, nil
}

func (d *DB) DeferFactoryIssue(ctx context.Context, epicID, issueID, reason string) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'deferred', outcome = '', outcome_reason = ? WHERE id = ? AND epic_id = ? AND status = 'open'`, reason, issueID, epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("factory issue cannot be deferred")
	}
	return nil
}

func (d *DB) ResumeFactoryIssue(ctx context.Context, epicID, issueID string) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome_reason = '' WHERE id = ? AND epic_id = ? AND status = 'deferred' AND NOT (EXISTS (SELECT 1 FROM factory_workflow_step w JOIN factory_issue i ON i.id = w.issue_id WHERE i.epic_id = factory_issue.epic_id) AND EXISTS (SELECT 1 FROM factory_issue delivery WHERE delivery.epic_id = factory_issue.epic_id AND delivery.kind = 'delivery' AND delivery.status = 'closed' AND delivery.outcome = 'succeeded' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = delivery.id)))`, issueID, epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("factory issue cannot be resumed")
	}
	return nil
}

func (d *DB) RetryFactoryIssueAt(ctx context.Context, epicID, issueID string, wakeAt time.Time) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'retry_wait', outcome = '', retry_at = ?, retry_attempts = retry_attempts + 1 WHERE id = ? AND epic_id = ? AND status = 'open'`, wakeAt.UnixMilli(), issueID, epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("factory issue cannot wait for retry")
	}
	return nil
}

func (d *DB) WakeFactoryRetries(ctx context.Context, at time.Time) error {
	_, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', retry_at = 0 WHERE status = 'retry_wait' AND retry_at <= ?`, at.UnixMilli())
	return err
}

// CloseFactoryMol closes a successful Mol. Open optional descendants are
// cancelled here so a closed container cannot later dispatch them.
func (d *DB) CloseFactoryMol(ctx context.Context, epicID, molID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var kind, status string
	if err := tx.QueryRowContext(ctx, `SELECT kind, status FROM factory_issue WHERE id = ? AND epic_id = ?`, molID, epicID).Scan(&kind, &status); err != nil {
		return fmt.Errorf("reading Factory Mol for closure: %w", err)
	}
	if kind != "mol" || status != "open" {
		return errors.New("factory Mol is unavailable for closure")
	}
	if err := closeFactoryDescendants(ctx, tx, molID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = '' WHERE id = ?`, molID); err != nil {
		return err
	}
	return tx.Commit()
}

func closeFactoryDescendants(ctx context.Context, tx *sql.Tx, molID string) error {
	const descendants = `WITH RECURSIVE descendants(id, required) AS (
		SELECT child_issue_id, requirement = 'required' FROM factory_issue_hierarchy WHERE parent_issue_id = ?
		UNION ALL
		SELECT h.child_issue_id, d.required AND h.requirement = 'required' FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id
	)`
	var activeOptional, incompleteRequired int
	if err := tx.QueryRowContext(ctx, descendants+` SELECT
		COALESCE(SUM(CASE WHEN required = 0 AND i.status = 'in_progress' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN required = 1 AND NOT (i.status = 'closed' AND i.outcome = 'succeeded' AND (i.kind <> 'gate' OR COALESCE(g.resolution, '') = 'approved')) THEN 1 ELSE 0 END), 0)
		FROM descendants d JOIN factory_issue i ON i.id = d.id LEFT JOIN factory_plan_gate g ON g.issue_id = i.id`, molID).Scan(&activeOptional, &incompleteRequired); err != nil {
		return err
	}
	if activeOptional != 0 {
		return errors.New("factory Mol has active optional work")
	}
	if incompleteRequired != 0 {
		return errors.New("factory Mol has incomplete required work")
	}
	var projectRequests int
	if err := tx.QueryRowContext(ctx, descendants+` SELECT COUNT(*) FROM descendants d JOIN factory_project_request_gate g ON g.work_item_id = d.id WHERE g.resolution NOT IN ('approved', 'rejected')`, molID).Scan(&projectRequests); err != nil {
		return err
	}
	if projectRequests != 0 {
		return errors.New("factory Mol has pending project requests")
	}
	_, err := tx.ExecContext(ctx, descendants+` UPDATE factory_issue SET status = 'closed', outcome = 'cancelled', outcome_reason = 'container_closed_without_execution'
		WHERE id IN (SELECT d.id FROM descendants d WHERE d.required = 0) AND status NOT IN ('closed', 'in_progress')`, molID)
	return err
}

// CloseFactoryEpic requires the root Mol to have been explicitly closed unless
// the operator explicitly overrides the remaining work guard.
func (d *DB) CloseFactoryEpic(ctx context.Context, epicID string, force bool) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_epic SET status = 'closed', updated_at = ?
		WHERE id = ? AND status IN ('open', 'paused') AND (? OR EXISTS (
			SELECT 1 FROM factory_issue i WHERE i.epic_id = factory_epic.id AND i.kind = 'mol' AND i.status = 'closed' AND i.outcome = 'succeeded'
				AND NOT EXISTS (SELECT 1 FROM factory_issue_hierarchy h WHERE h.child_issue_id = i.id)
		))`, time.Now().UnixMilli(), epicID, force)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("%w: close the root Mol successfully before closing the Epic", model.ErrEpicClosureBlocked)
	}
	return nil
}

func (d *DB) SetFactoryEpicPaused(ctx context.Context, epicID string, paused bool) error {
	status := "paused"
	if !paused {
		status = "open"
	}
	result, err := d.db.ExecContext(ctx, `UPDATE factory_epic SET status = ?, updated_at = ? WHERE id = ? AND status IN ('open', 'paused')`, status, time.Now().UnixMilli(), epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		if _, getErr := d.GetFactoryEpic(ctx, epicID); errors.Is(getErr, model.ErrNativeEpicNotFound) {
			return getErr
		}
		return errors.New("factory Epic cannot be paused or resumed")
	}
	return nil
}
