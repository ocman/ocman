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

// ClaimFactoryImplementation atomically reserves implementation capacity,
// claims an executable Issue, and creates its durable launch record.
func (d *DB) ClaimFactoryImplementation(ctx context.Context, epicID, issueID, profile string, at time.Time) (model.NativeEpic, model.FactoryAttempt, error) {
	if profile != "factory-implement/v1" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation requires factory-implement/v1")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	epic, err := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE id = ?`, epicID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, model.ErrNativeEpicNotFound
	}
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if epic.Status != "open" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory Epic is closed")
	}
	var kind, status, project string
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(parent_id, requirement) AS (
		SELECT parent_issue_id, requirement FROM factory_issue_hierarchy WHERE child_issue_id = ?
		UNION ALL
		SELECT h.parent_issue_id, h.requirement FROM factory_issue_hierarchy h JOIN ancestors a ON h.child_issue_id = a.parent_id
		) SELECT i.kind, i.status, i.project_path FROM factory_issue i WHERE i.id = ? AND i.epic_id = ?
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)
		AND EXISTS (SELECT 1 FROM factory_epic_project p WHERE p.epic_id = i.epic_id AND p.project_path = i.project_path)
		AND NOT EXISTS (SELECT 1 FROM ancestors WHERE requirement = 'reference')
		AND NOT EXISTS (
			SELECT 1 FROM factory_issue_dependency d
			JOIN factory_issue b ON b.id = d.depends_on_issue_id
			LEFT JOIN factory_plan_gate g ON g.issue_id = b.id
			WHERE d.issue_id = i.id
			AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = b.id)
			AND NOT (
				(d.type = 'blocks' AND b.status = 'closed' AND b.outcome = 'succeeded' AND (b.kind <> 'gate' OR g.resolution = 'approved'))
				OR (d.type = 'on_failure' AND b.status = 'closed' AND ((b.kind <> 'gate' AND b.outcome = 'failed') OR (b.kind = 'gate' AND g.resolution = 'rejected')))
				OR (d.type = 'merge_gated' AND b.kind = 'delivery' AND EXISTS (SELECT 1 FROM factory_merge_gate_observation o WHERE o.delivery_issue_id = b.id AND o.status = 'merged'))
			)
		)`, issueID, issueID, epicID).Scan(&kind, &status, &project); err != nil || (kind != "implementation" && kind != "task" && kind != "delivery") || status != "open" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation issue is not ready")
	}
	var acknowledged int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_local_execution_ack WHERE host_id = 'local' AND repo_root = ? AND profile_id = 'factory-implement' AND profile_version = 'v1'`, project).Scan(&acknowledged); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation requires local execution acknowledgement")
	}
	if err := validateFactoryDeliveryOrder(ctx, tx, epicID, project, kind); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	policy := model.FactoryCapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4}
	_ = tx.QueryRowContext(ctx, `SELECT global_capacity, project_capacity FROM factory_capacity_policy WHERE id = 1`).Scan(&policy.GlobalCapacity, &policy.ProjectCapacity)
	var override int
	if err := tx.QueryRowContext(ctx, `SELECT capacity FROM factory_project_capacity_override WHERE project_path = ?`, project).Scan(&override); err == nil {
		policy.ProjectCapacity = override
	}
	var global, projectActive int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN json_extract(frozen_policy_json, '$.repository') = ? THEN 1 ELSE 0 END), 0) FROM factory_attempt a WHERE phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1' AND NOT EXISTS (SELECT 1 FROM factory_recovery_gate r WHERE r.attempt_id = a.id AND r.resolution = 'open') AND NOT EXISTS (SELECT 1 FROM factory_authority_escalation_gate g WHERE g.attempt_id = a.id AND g.resolution NOT IN ('approve', 'reject')) AND NOT EXISTS (SELECT 1 FROM factory_project_request_gate p WHERE p.attempt_id = a.id AND p.resolution NOT IN ('approved', 'rejected'))`, project).Scan(&global, &projectActive); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	var epicActive int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_attempt WHERE epic_id = ? AND phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1'`, epicID).Scan(&epicActive); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if epicActive != 0 {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory Epic workspace is in use")
	}
	if global >= policy.GlobalCapacity || projectActive >= policy.ProjectCapacity {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation capacity is full")
	}
	changed, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'in_progress' WHERE id = ? AND status = 'open'`, issueID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	n, _ := changed.RowsAffected()
	if n != 1 {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation issue is not ready")
	}
	var sequence int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM factory_attempt WHERE work_item_id = ?`, issueID).Scan(&sequence); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	id, err := factoryGraphID("fa_")
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	agentToken, err := factoryGraphID("fat_")
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	attemptPolicy := model.FactoryAttemptPolicy{Repository: project, Profile: profile}
	var previousPolicy string
	if err := tx.QueryRowContext(ctx, `SELECT frozen_policy_json FROM factory_attempt WHERE epic_id = ? AND json_extract(frozen_policy_json, '$.repository') = ? AND json_extract(frozen_policy_json, '$.branch') <> '' ORDER BY created_at DESC, rowid DESC LIMIT 1`, epicID, project).Scan(&previousPolicy); err == nil {
		if err := json.Unmarshal([]byte(previousPolicy), &attemptPolicy); err != nil {
			return model.NativeEpic{}, model.FactoryAttempt{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	var successorLineage bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM factory_issue current
		WHERE current.id = (SELECT id FROM factory_issue WHERE epic_id = ? AND project_path = ? AND kind = 'delivery' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id) ORDER BY created_at DESC, id DESC LIMIT 1)
		AND NOT EXISTS (SELECT 1 FROM factory_attempt a WHERE a.epic_id = current.epic_id AND json_extract(a.frozen_policy_json, '$.repository') = current.project_path AND a.created_at >= current.created_at)
		AND EXISTS (SELECT 1 FROM factory_issue prior JOIN factory_merge_gate_observation observation ON observation.delivery_issue_id = prior.id AND observation.status = 'merged' WHERE prior.epic_id = current.epic_id AND prior.project_path = current.project_path AND prior.kind = 'delivery' AND prior.id <> current.id))`, epicID, project).Scan(&successorLineage); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if successorLineage {
		attemptPolicy.Branch, attemptPolicy.BaseRef, attemptPolicy.CheckpointSHA = "", "", ""
	}
	attemptPolicy.Repository = project
	projects, err := listFactoryEpicProjectsWith(ctx, tx, epicID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	attemptPolicy.Projects = make([]string, 0, len(projects))
	for _, admitted := range projects {
		attemptPolicy.Projects = append(attemptPolicy.Projects, admitted.Path)
	}
	attemptPolicy.Delivery = kind == "delivery"
	if err := tx.QueryRowContext(ctx, `SELECT implementation_model FROM factory_plan_gate WHERE epic_id = ?`, epicID).Scan(&attemptPolicy.Model); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	workflowIssues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	for _, issue := range workflowIssues {
		if issue.ID == issueID && issue.Workflow != nil && issue.Workflow.Config.Model != "" {
			attemptPolicy.Model = issue.Workflow.Config.Model
		}
	}
	if !successorLineage {
		if err := tx.QueryRowContext(ctx, `SELECT json_extract(CASE WHEN json_valid(result_json) THEN result_json ELSE '{}' END, '$.commitSha') FROM factory_attempt WHERE epic_id = ? AND json_extract(frozen_policy_json, '$.repository') = ? AND terminal_outcome = 'succeeded' AND json_extract(CASE WHEN json_valid(result_json) THEN result_json ELSE '{}' END, '$.commitSha') <> '' ORDER BY finished_at DESC, rowid DESC LIMIT 1`, epicID, project).Scan(&attemptPolicy.CheckpointSHA); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return model.NativeEpic{}, model.FactoryAttempt{}, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT
		json_extract(frozen_policy_json, '$.deliveryRemoteType'),
		json_extract(frozen_policy_json, '$.deliveryRemoteHost'),
		json_extract(frozen_policy_json, '$.deliveryRemoteRepo')
		FROM factory_attempt WHERE epic_id = ? AND json_extract(frozen_policy_json, '$.repository') = ? AND json_extract(frozen_policy_json, '$.deliveryRemoteRepo') <> ''
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, epicID, project).Scan(&attemptPolicy.DeliveryRemoteType, &attemptPolicy.DeliveryRemoteHost, &attemptPolicy.DeliveryRemoteRepo); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	// Carry forward the Epic-level permission rules into every attempt policy.
	var epicRulesJSON string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(permission_rules_json, '[]') FROM factory_epic WHERE id = ?`, epicID).Scan(&epicRulesJSON); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if epicRulesJSON != "" && epicRulesJSON != "[]" {
		_ = json.Unmarshal([]byte(epicRulesJSON), &attemptPolicy.PermissionRules)
	}
	policyJSON, err := json.Marshal(attemptPolicy)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	now := at.UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at) VALUES (?, ?, ?, ?, 'prepared', ?, ?, ?)`, id, epicID, issueID, sequence, string(policyJSON), now, now); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at) VALUES ('factory', 'attempt_token', ?, 'attempt', ?, '{}', ?)`, agentToken, id, now); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	return epic, model.FactoryAttempt{ID: id, EpicID: epicID, WorkID: issueID, Sequence: sequence, Phase: model.FactoryAttemptPrepared, FrozenPolicy: attemptPolicy, CreatedAt: now, UpdatedAt: now, AgentToken: agentToken}, nil
}

// ClaimFactoryPlan marks one poured Plan as claimed and allocates its attempt together.
func (d *DB) ClaimFactoryPlan(ctx context.Context, epicID, issueID, profile string, at time.Time) (model.NativeEpic, model.FactoryAttempt, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("beginning Factory Plan claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	epic, err := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE id = ?`, epicID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, model.ErrNativeEpicNotFound
	}
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("getting Factory Epic for Plan claim: %w", err)
	}
	epic.Projects, err = listFactoryEpicProjectsWith(ctx, tx, epicID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if epic.Status != "open" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory Epic is closed")
	}
	var kind, status string
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(parent_id, requirement) AS (
		SELECT parent_issue_id, requirement FROM factory_issue_hierarchy WHERE child_issue_id = ?
		UNION ALL
		SELECT h.parent_issue_id, h.requirement FROM factory_issue_hierarchy h JOIN ancestors a ON h.child_issue_id = a.parent_id
	) SELECT i.kind, i.status FROM factory_issue i WHERE i.id = ? AND i.epic_id = ?
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)
		AND NOT EXISTS (SELECT 1 FROM ancestors WHERE requirement = 'reference')`, issueID, issueID, epicID).Scan(&kind, &status); errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory plan issue not found")
	} else if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("getting Factory Plan Issue: %w", err)
	}
	if kind != "plan" || status != "open" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory plan issue is not ready")
	}
	claimed, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'in_progress' WHERE id = ? AND status = 'open'`, issueID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("claiming Factory Plan Issue: %w", err)
	}
	changed, err := claimed.RowsAffected()
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("checking Factory Plan claim: %w", err)
	}
	if changed != 1 {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory plan issue is not ready")
	}
	var sequence int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM factory_attempt WHERE work_item_id = ?`, issueID).Scan(&sequence); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("allocating Factory Plan attempt sequence: %w", err)
	}
	id, err := factoryGraphID("fa_")
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	agentToken, err := factoryGraphID("fat_")
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	now := at.UnixMilli()
	policy := model.FactoryAttemptPolicy{Repository: epic.InitialProject, Profile: profile}
	steps, err := workflowSteps(ctx, tx, epicID)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if step := steps[issueID]; step != nil {
		policy.Model = step.Config.Model
	}
	// Carry forward the Epic-level permission rules into the planning attempt.
	var epicRulesJSON string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(permission_rules_json, '[]') FROM factory_epic WHERE id = ?`, epicID).Scan(&epicRulesJSON); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("reading Epic permission rules for Plan claim: %w", err)
	}
	if epicRulesJSON != "" && epicRulesJSON != "[]" {
		_ = json.Unmarshal([]byte(epicRulesJSON), &policy.PermissionRules)
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("encoding Factory Plan policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at) VALUES (?, ?, ?, ?, 'prepared', ?, ?, ?)`, id, epicID, issueID, sequence, string(policyJSON), now, now); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("creating Factory Plan attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at) VALUES ('factory', 'attempt_token', ?, 'attempt', ?, '{}', ?)`, agentToken, id, now); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("recording Factory Plan attempt token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, fmt.Errorf("committing Factory Plan claim: %w", err)
	}
	return epic, model.FactoryAttempt{ID: id, EpicID: epicID, WorkID: issueID, Sequence: sequence, Phase: model.FactoryAttemptPrepared, FrozenPolicy: policy, CreatedAt: now, UpdatedAt: now, AgentToken: agentToken}, nil
}
