package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// MutateFactoryGraph applies graph edits atomically. In-progress and closed
// Issues are immutable; all other lifecycle states remain editable.
func (d *DB) MutateFactoryGraph(ctx context.Context, m model.GraphMutation) error {
	if m.Action == "approve_step" || m.Action == "reject_step" {
		return d.decideWorkflowStep(ctx, m)
	}
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if m.EpicID == "" {
		return invalid("factory epic is required for structural mutation")
	}
	var epicStatus, epicProject string
	if err := tx.QueryRowContext(ctx, `SELECT status, project_path FROM factory_epic WHERE id = ?`, m.EpicID).Scan(&epicStatus, &epicProject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("factory epic is unavailable for structural mutation")
		}
		return fmt.Errorf("reading Factory Epic for structural mutation: %w", err)
	}
	if epicStatus != "open" {
		return invalid("factory epic is unavailable for structural mutation")
	}
	if m.Action == "create" && m.Project == "" {
		m.Project = epicProject
	}
	if m.Project != "" {
		var admitted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_epic_project WHERE epic_id = ? AND project_path = ?)`, m.EpicID, m.Project).Scan(&admitted); err != nil {
			return err
		}
		if !admitted {
			return invalid(fmt.Sprintf("project target %q is not admitted to Epic %s", m.Project, m.EpicID))
		}
	}
	project, issueKind := m.Project, m.Kind
	if m.Action != "create" {
		if err := tx.QueryRowContext(ctx, `SELECT project_path, kind FROM factory_issue WHERE id = ? AND epic_id = ?`, m.IssueID, m.EpicID).Scan(&project, &issueKind); err != nil {
			return invalid("factory issue is unavailable for structural mutation")
		}
	}
	lockID := m.IssueID
	if m.Action == "create" {
		lockID = m.ParentID
	}
	var scopeLocked bool
	lockQuery := `SELECT EXISTS(
			SELECT 1 FROM factory_project_request_gate g
			JOIN factory_issue p ON p.id = g.plan_issue_id
			WHERE (g.work_item_id = ? OR g.plan_issue_id = ?) AND g.resolution = 'approved' AND p.status IN ('open', 'in_progress'))`
	lockArgs := []any{lockID, lockID}
	if m.Action == "reparent" || m.Action == "delete" {
		lockQuery = `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id)
			SELECT EXISTS(SELECT 1 FROM factory_project_request_gate g JOIN factory_issue p ON p.id = g.plan_issue_id WHERE (g.work_item_id IN (SELECT id FROM descendants) OR g.plan_issue_id IN (SELECT id FROM descendants)) AND g.resolution = 'approved' AND p.status IN ('open', 'in_progress'))`
		lockArgs = []any{lockID}
	}
	if err := tx.QueryRowContext(ctx, lockQuery, lockArgs...).Scan(&scopeLocked); err != nil {
		return err
	}
	if !scopeLocked && m.Action == "reparent" {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_project_request_gate g JOIN factory_issue p ON p.id = g.plan_issue_id WHERE (g.work_item_id = ? OR g.plan_issue_id = ?) AND g.resolution = 'approved' AND p.status IN ('open', 'in_progress'))`, m.ParentID, m.ParentID).Scan(&scopeLocked); err != nil {
			return err
		}
	}
	if scopeLocked {
		return invalid("factory issue is awaiting scoped replanning")
	}
	movedProject := ""
	if m.Action == "edit" {
		movedProject = m.Project
	}
	var delivering int
	deliveryLock := "(status = 'in_progress' OR (status = 'closed' AND outcome = 'succeeded'))"
	if m.Action == "create" {
		deliveryLock = "status = 'in_progress'"
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_issue WHERE epic_id = ? AND project_path IN (?, ?) AND kind = 'delivery' AND `+deliveryLock+` AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, m.EpicID, project, movedProject).Scan(&delivering); err != nil {
		return err
	}
	if delivering != 0 {
		return invalid("factory project delivery has started; finish or recover delivery before changing that project")
	}
	if m.Action == "reparent" || m.Action == "delete" {
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id)
			SELECT COUNT(*) FROM factory_issue i JOIN factory_issue delivery ON delivery.epic_id = i.epic_id AND delivery.project_path = i.project_path AND delivery.kind = 'delivery'
			WHERE i.id IN (SELECT id FROM descendants) AND (delivery.status = 'in_progress' OR (delivery.status = 'closed' AND delivery.outcome = 'succeeded'))
			AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = delivery.id)`, m.IssueID).Scan(&delivering); err != nil {
			return err
		}
		if delivering != 0 {
			return invalid("factory project delivery has started; finish or recover delivery before changing that project")
		}
	}
	openIssue := func(id string, local bool) (string, error) {
		var epicID, status string
		err := tx.QueryRowContext(ctx, `SELECT epic_id, status FROM factory_issue WHERE id = ? AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, id).Scan(&epicID, &status)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", invalid("factory issue is unavailable for structural mutation")
			}
			return "", fmt.Errorf("reading Factory Issue for structural mutation: %w", err)
		}
		if (local && epicID != m.EpicID) || status == "in_progress" || status == "closed" {
			return "", invalid("factory issue is unavailable for structural mutation")
		}
		return epicID, nil
	}
	if m.Action == "create" {
		var workflow, implementationParent bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_workflow_step w JOIN factory_issue i ON i.id = w.issue_id WHERE i.epic_id = ?), EXISTS(SELECT 1 FROM factory_issue WHERE id = ? AND kind = 'phase')`, m.EpicID, m.ParentID).Scan(&workflow, &implementationParent); err != nil {
			return err
		}
		if workflow && !implementationParent {
			return invalid("add workflow implementation tasks under the implementation phase")
		}
		if _, err := openIssue(m.ParentID, true); err != nil {
			return err
		}
		if m.Kind != "mol" && m.Kind != "task" && m.Kind != "implementation" {
			return invalid("invalid factory issue kind")
		}
		if strings.TrimSpace(m.Title) == "" {
			return invalid("factory issue title is required")
		}
		epicID := m.EpicID
		id, err := factoryChildID(ctx, tx, m.ParentID)
		if err != nil {
			return err
		}
		index, err := factoryChildIndex(id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, ?, ?, ?, 'open', ?)`, id, epicID, m.Project, m.Kind, m.Title, m.Description, time.Now().UnixMilli()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, ?)`, m.ParentID, id, index, requiredMutationRequirement(m.Requirement)); err != nil {
			return err
		}
		if m.Kind != "mol" {
			if err = closeHandBuiltMaterializationTx(ctx, tx, epicID); err != nil {
				return err
			}
		}
		m.IssueID = id
	} else {
		var declared bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_workflow_step WHERE issue_id = ?)`, m.IssueID).Scan(&declared); err != nil {
			return err
		}
		if declared {
			return invalid("workflow steps are pinned; edit the Formula for a new Epic")
		}
		if _, err := openIssue(m.IssueID, true); err != nil {
			return err
		}
		switch m.Action {
		case "edit":
			if strings.TrimSpace(m.Title) == "" {
				return invalid("factory issue title is required")
			}
			if m.Project == "" {
				_, err = tx.ExecContext(ctx, `UPDATE factory_issue SET title = ?, description = ? WHERE id = ?`, m.Title, m.Description, m.IssueID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE factory_issue SET title = ?, description = ?, project_path = ? WHERE id = ?`, m.Title, m.Description, m.Project, m.IssueID)
			}
		case "reparent":
			if m.IssueID == m.ParentID {
				return invalid("factory issue cannot be its own parent")
			}
			if _, err = openIssue(m.ParentID, true); err == nil {
				var childEpic, parentEpic string
				err = tx.QueryRowContext(ctx, `SELECT epic_id FROM factory_issue WHERE id = ?`, m.IssueID).Scan(&childEpic)
				if err == nil {
					err = tx.QueryRowContext(ctx, `SELECT epic_id FROM factory_issue WHERE id = ?`, m.ParentID).Scan(&parentEpic)
				}
				if err == nil && childEpic != parentEpic {
					err = invalid("factory hierarchy cannot cross Work Epics")
				}
				if err == nil {
					var cycle bool
					if cycle, err = factoryHierarchyCycle(ctx, tx, m.IssueID, m.ParentID); err == nil && cycle {
						err = invalid("factory hierarchy creates a cycle")
					}
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE factory_issue_hierarchy SET parent_issue_id = ?, requirement = ? WHERE child_issue_id = ?`, m.ParentID, requiredMutationRequirement(m.Requirement), m.IssueID)
				}
			}
		case "link":
			if m.IssueID == m.DependsOnID {
				return invalid("factory issue cannot depend on itself")
			}
			var blockerKind, blockerProject string
			if m.DependencyType == "merge_gated" {
				err = tx.QueryRowContext(ctx, `SELECT kind, project_path FROM factory_issue WHERE id = ? AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, m.DependsOnID).Scan(&blockerKind, &blockerProject)
				if err != nil || blockerKind != "delivery" || (issueKind != "implementation" && issueKind != "task") || project == blockerProject {
					err = invalid("merge-gated dependency must target a Project Delivery")
				}
			} else {
				_, err = openIssue(m.DependsOnID, false)
			}
			if err == nil {
				var cycle bool
				if m.DependencyType != "blocks" && m.DependencyType != "on_failure" && m.DependencyType != "merge_gated" {
					err = invalid("invalid Factory dependency type")
				} else if cycle, err = factoryDependencyCycle(ctx, tx, m.IssueID, m.DependsOnID); err == nil && cycle {
					err = invalid("factory dependency creates a cycle")
				} else if err == nil {
					_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, ?)`, m.IssueID, m.DependsOnID, m.DependencyType)
				}
			}
		case "unlink":
			_, err = tx.ExecContext(ctx, `DELETE FROM factory_issue_dependency WHERE issue_id = ? AND depends_on_issue_id = ? AND type = ?`, m.IssueID, m.DependsOnID, m.DependencyType)
		case "delete":
			var started int
			err = tx.QueryRowContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id) SELECT count(*) FROM factory_issue WHERE id IN (SELECT id FROM descendants) AND status IN ('in_progress', 'closed')`, m.IssueID).Scan(&started)
			if err == nil && started != 0 {
				err = invalid("factory issue is unavailable for structural mutation")
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id) INSERT INTO factory_removed_issue (issue_id, plan_id, plan_revision, removed_at) SELECT id, epic_id, 0, ? FROM factory_issue WHERE id IN (SELECT id FROM descendants) ON CONFLICT(issue_id) DO NOTHING`, m.IssueID, time.Now().UnixMilli())
			}
		default:
			err = invalid("unknown Factory graph mutation")
		}
	}
	if err != nil {
		return err
	}
	details, _ := json.Marshal(m)
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, m.EpicID, m.IssueID, m.Actor, "graph."+m.Action, string(details), time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// closeHandBuiltMaterializationTx satisfies an approved epic's open
// Materialization when executable work was added by hand (mutate_graph)
// rather than by MaterializeFactoryPlan. Without this the tracer's required
// materialization node blocks closure forever, since nothing else closes it.
func closeHandBuiltMaterializationTx(ctx context.Context, tx *sql.Tx, epicID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = 'Work graph built by hand'
		WHERE epic_id = ? AND kind = 'materialization' AND status = 'open'
		AND EXISTS (SELECT 1 FROM factory_plan_gate g WHERE g.epic_id = factory_issue.epic_id AND g.resolution = 'approved')
		AND EXISTS (SELECT 1 FROM factory_issue w WHERE w.epic_id = factory_issue.epic_id AND w.kind IN ('task', 'implementation')
			AND NOT EXISTS (SELECT 1 FROM factory_removed_issue r WHERE r.issue_id = w.id)
			AND NOT EXISTS (WITH RECURSIVE lineage(id) AS (SELECT w.id UNION ALL SELECT h.parent_issue_id FROM factory_issue_hierarchy h JOIN lineage ON h.child_issue_id = lineage.id)
				SELECT 1 FROM lineage JOIN factory_materialization_provenance p ON p.entity_kind = 'issue' AND p.entity_id = lineage.id))`, epicID)
	if err != nil {
		return fmt.Errorf("closing hand-built Factory materialization: %w", err)
	}
	return nil
}

// closePlanOnApprovalTx closes the epic's Plan work and terminates its
// planning attempts: once the proposal is approved there is nothing left
// for the Plan to do, and an in_progress Plan is immutable everywhere else.
func closePlanOnApprovalTx(ctx context.Context, tx *sql.Tx, epicID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = ''
		WHERE epic_id = ? AND kind = 'plan' AND status IN ('open', 'in_progress')`, epicID); err != nil {
		return fmt.Errorf("closing approved Factory Plan: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'succeeded', finished_at = ?, updated_at = ?
		WHERE epic_id = ? AND phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-plan/v1'`, now, now, epicID); err != nil {
		return fmt.Errorf("completing approved Factory Plan attempt: %w", err)
	}
	return nil
}

// ReopenFactoryIssue returns failed or cancelled executable work to the
// dispatch queue with a fresh retry budget. It is the human escape hatch for
// work that exhausted its launch retries.
func (d *DB) ReopenFactoryIssue(ctx context.Context, epicID, issueID string) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '', outcome_reason = '', retry_attempts = 0, retry_at = 0
		WHERE id = ? AND epic_id = ? AND kind IN ('task', 'implementation', 'delivery') AND status = 'closed' AND outcome IN ('failed', 'cancelled')
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)
		AND EXISTS (SELECT 1 FROM factory_epic WHERE id = ? AND status = 'open')`, issueID, epicID, epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("factory issue cannot be reopened")
	}
	return nil
}

func requiredMutationRequirement(requirement string) string {
	if requirement == "optional" || requirement == "reference" {
		return requirement
	}
	return "required"
}

// factoryDependencyCycle reports whether issueID is already reachable from
// blockerID through dependency edges; the seed row makes a self-edge a cycle.
func factoryDependencyCycle(ctx context.Context, tx *sql.Tx, issueID, blockerID string) (bool, error) {
	return factoryReachable(ctx, tx, `WITH RECURSIVE reachable(id) AS (SELECT ? UNION SELECT d.depends_on_issue_id FROM factory_issue_dependency d JOIN reachable r ON d.issue_id = r.id) SELECT 1 FROM reachable WHERE id = ?`, blockerID, issueID)
}

// factoryHierarchyCycle reports whether parentID is issueID or one of its
// descendants, so reparenting onto it would close a loop.
func factoryHierarchyCycle(ctx context.Context, tx *sql.Tx, issueID, parentID string) (bool, error) {
	return factoryReachable(ctx, tx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id) SELECT 1 FROM descendants WHERE id = ?`, issueID, parentID)
}

func factoryReachable(ctx context.Context, tx *sql.Tx, query, from, to string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, query, from, to).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func factoryIssueRequirement(issue *model.NativeIssue, byID map[string]*model.NativeIssue) string {
	requirement := issue.Requirement
	if requirement == "reference" {
		return requirement
	}
	for parent := byID[issue.ParentID]; parent != nil; parent = byID[parent.ParentID] {
		if parent.Requirement == "reference" {
			return "reference"
		}
		if parent.Requirement == "optional" {
			requirement = "optional"
		}
	}
	return requirement
}
