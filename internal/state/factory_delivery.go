package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) ListFactoryMergeGateDeliveries(ctx context.Context, observedBefore time.Time, limit int) ([]model.FactoryDeliveryObservation, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT delivery.id, a.id,
		json_extract(a.result_json, '$.prUrl'), json_extract(a.result_json, '$.commitSha'), a.frozen_policy_json
		FROM factory_issue_dependency dependency
		JOIN factory_issue delivery ON delivery.id = dependency.depends_on_issue_id AND delivery.kind = 'delivery'
		JOIN factory_attempt a ON a.work_item_id = delivery.id AND a.terminal_outcome = 'succeeded'
		LEFT JOIN factory_merge_gate_observation observation ON observation.delivery_issue_id = delivery.id
		WHERE dependency.type = 'merge_gated' AND json_extract(a.result_json, '$.prUrl') <> ''
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id IN (dependency.issue_id, delivery.id))
		AND (observation.delivery_issue_id IS NULL OR (observation.status <> 'merged' AND observation.observed_at <= ?))
		AND NOT EXISTS (SELECT 1 FROM factory_attempt newer WHERE newer.work_item_id = delivery.id AND newer.terminal_outcome = 'succeeded' AND (newer.finished_at > a.finished_at OR (newer.finished_at = a.finished_at AND newer.rowid > a.rowid)))
		GROUP BY delivery.id ORDER BY delivery.created_at LIMIT ?`, observedBefore.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var observations []model.FactoryDeliveryObservation
	for rows.Next() {
		var observation model.FactoryDeliveryObservation
		var policyJSON string
		if err := rows.Scan(&observation.DeliveryIssueID, &observation.AttemptID, &observation.PRURL, &observation.CommitSHA, &policyJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(policyJSON), &observation.Policy); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func (d *DB) RecordFactoryDeliveryObservation(ctx context.Context, observation model.FactoryDeliveryObservation) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO factory_merge_gate_observation (delivery_issue_id, attempt_id, pr_url, commit_sha, status, reason, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(delivery_issue_id) DO UPDATE SET attempt_id = excluded.attempt_id, pr_url = excluded.pr_url, commit_sha = excluded.commit_sha, status = excluded.status, reason = excluded.reason, observed_at = excluded.observed_at
		WHERE factory_merge_gate_observation.status <> 'merged' AND factory_merge_gate_observation.observed_at <= excluded.observed_at`, observation.DeliveryIssueID, observation.AttemptID, observation.PRURL, observation.CommitSHA, observation.Status, observation.Reason, observation.ObservedAt)
	return err
}

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
func validateFactoryDeliveryOrder(ctx context.Context, tx *sql.Tx, epicID, project, kind string) error {
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return err
	}
	byID := make(map[string]*model.NativeIssue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}
	for _, issue := range issues {
		if issue.Project != project {
			continue
		}
		if issue.Kind == "delivery" && issue.Status == "closed" && issue.Outcome == "succeeded" {
			return errors.New("factory project delivery is already complete")
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
	type projectDelivery struct {
		delivery   string
		parent     string
		blockers   []string
		executable bool
	}
	projects := map[string]*projectDelivery{}
	byID := make(map[string]*model.NativeIssue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}
	for _, issue := range issues {
		if issue.Kind == "delivery" {
			project := projects[issue.Project]
			if project == nil {
				project = &projectDelivery{}
				projects[issue.Project] = project
			}
			project.delivery = issue.ID
			continue
		}
		requirement := factoryIssueRequirement(&issue, byID)
		if requirement != "reference" && (issue.Kind == "implementation" || issue.Kind == "task") && issue.DispatchState != "not_applicable" {
			project := projects[issue.Project]
			if project == nil {
				project = &projectDelivery{}
				projects[issue.Project] = project
			}
			project.parent = issue.ParentID
			project.executable = true
		}
		if requirement != "required" || issue.DispatchState == "not_applicable" || issue.Kind == "mol" || (issue.Kind == "gate" && issue.GateResolution == "") {
			continue
		}
		project := projects[issue.Project]
		if project == nil {
			project = &projectDelivery{}
			projects[issue.Project] = project
		}
		project.blockers = append(project.blockers, issue.ID)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id, project_path FROM factory_issue WHERE epic_id = ? AND kind = 'delivery' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)`, epicID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var delivery, projectPath string
		if err := rows.Scan(&delivery, &projectPath); err != nil {
			rows.Close()
			return err
		}
		project := projects[projectPath]
		if project == nil {
			project = &projectDelivery{}
			projects[projectPath] = project
		}
		project.delivery = delivery
	}
	if err := rows.Close(); err != nil {
		return err
	}
	paths := make([]string, 0, len(projects))
	for path, project := range projects {
		if project.executable {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		project := projects[path]
		if project.delivery == "" {
			project.delivery, err = factoryChildID(ctx, tx, project.parent)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, 'delivery', 'Deliver the completed work', 'Review the project changes, run the required checks, and create or reuse its pull request.', 'open', ?)`, project.delivery, epicID, path, time.Now().UnixMilli()); err != nil {
				return err
			}
			index, err := factoryChildIndex(project.delivery)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, project.parent, project.delivery, index); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM factory_issue_dependency WHERE issue_id = ?`, project.delivery); err != nil {
			return err
		}
		for _, blocker := range project.blockers {
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks') ON CONFLICT DO NOTHING`, project.delivery, blocker); err != nil {
				return err
			}
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
