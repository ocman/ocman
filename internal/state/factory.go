package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

const factoryAttemptColumns = `id, epic_id, work_item_id, sequence, phase, terminal_outcome,
	session_platform, session_id, retry_of_attempt_id, retry_at, frozen_policy_json,
	result_json, failure_type, failure_message, created_at, updated_at, started_at, finished_at`

// GetFactoryCapacityPolicy returns the configured policy, filling absent values
// with the service-level defaults.
func (d *DB) GetFactoryCapacityPolicy(ctx context.Context) (model.FactoryCapacityPolicy, error) {
	policy := model.FactoryCapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{}}
	err := d.db.QueryRowContext(ctx, `SELECT global_capacity, project_capacity FROM factory_capacity_policy WHERE id = 1`).Scan(&policy.GlobalCapacity, &policy.ProjectCapacity)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.FactoryCapacityPolicy{}, fmt.Errorf("getting Factory capacity policy: %w", err)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT project_path, capacity FROM factory_project_capacity_override ORDER BY project_path`)
	if err != nil {
		return model.FactoryCapacityPolicy{}, fmt.Errorf("listing Factory project capacity overrides: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var capacity int
		if err := rows.Scan(&path, &capacity); err != nil {
			return model.FactoryCapacityPolicy{}, fmt.Errorf("scanning Factory project capacity override: %w", err)
		}
		policy.ProjectOverrides[path] = capacity
	}
	if err := rows.Err(); err != nil {
		return model.FactoryCapacityPolicy{}, fmt.Errorf("listing Factory project capacity overrides: %w", err)
	}
	return policy, nil
}

// SetFactoryCapacityPolicy atomically replaces the policy and all overrides.
func (d *DB) SetFactoryCapacityPolicy(ctx context.Context, policy model.FactoryCapacityPolicy) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning Factory capacity policy update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_capacity_policy (id, global_capacity, project_capacity) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET global_capacity = excluded.global_capacity, project_capacity = excluded.project_capacity`, policy.GlobalCapacity, policy.ProjectCapacity); err != nil {
		return fmt.Errorf("saving Factory capacity policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM factory_project_capacity_override`); err != nil {
		return fmt.Errorf("clearing Factory project capacity overrides: %w", err)
	}
	for path, capacity := range policy.ProjectOverrides {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_project_capacity_override (project_path, capacity) VALUES (?, ?)`, path, capacity); err != nil {
			return fmt.Errorf("saving Factory project capacity override: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing Factory capacity policy update: %w", err)
	}
	return nil
}

// CreatePreparedFactoryAttempt durably allocates the next sequence for a Work Item.
func (d *DB) CreatePreparedFactoryAttempt(ctx context.Context, epicID, workID string, policy model.FactoryAttemptPolicy, at time.Time) (model.FactoryAttempt, error) {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("encoding Factory attempt policy: %w", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("generating Factory attempt ID: %w", err)
	}
	id := "fa_" + hex.EncodeToString(random[:])
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("beginning Factory attempt creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var sequence int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM factory_attempt WHERE work_item_id = ?`, workID).Scan(&sequence); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("allocating Factory attempt sequence: %w", err)
	}
	now := at.UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_attempt
		(id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'prepared', ?, ?, ?)`, id, epicID, workID, sequence, string(policyJSON), now, now); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("creating prepared Factory attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("committing prepared Factory attempt: %w", err)
	}
	return model.FactoryAttempt{
		ID: id, EpicID: epicID, WorkID: workID, Sequence: sequence,
		Phase: model.FactoryAttemptPrepared, FrozenPolicy: policy,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// GetFactoryAttempt returns one durable attempt by ID.
func (d *DB) GetFactoryAttempt(ctx context.Context, id string) (model.FactoryAttempt, bool, error) {
	attempt, err := scanFactoryAttempt(d.db.QueryRowContext(ctx, `SELECT `+factoryAttemptColumns+` FROM factory_attempt WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.FactoryAttempt{}, false, nil
	}
	if err != nil {
		return model.FactoryAttempt{}, false, fmt.Errorf("getting Factory attempt: %w", err)
	}
	return attempt, true, nil
}

// ListFactoryAttempts returns attempts in durable creation order. An empty epic ID lists all epics.
func (d *DB) ListFactoryAttempts(ctx context.Context, epicID string) ([]model.FactoryAttempt, error) {
	query := `SELECT ` + factoryAttemptColumns + ` FROM factory_attempt`
	var args []any
	if epicID != "" {
		query += ` WHERE epic_id = ?`
		args = append(args, epicID)
	}
	rows, err := d.db.QueryContext(ctx, query+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing Factory attempts: %w", err)
	}
	defer rows.Close()
	var attempts []model.FactoryAttempt
	for rows.Next() {
		attempt, err := scanFactoryAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning Factory attempt: %w", err)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing Factory attempts: %w", err)
	}
	return attempts, nil
}

// ActivateFactoryAttempt conditionally advances a prepared attempt.
func (d *DB) ActivateFactoryAttempt(ctx context.Context, id string, session model.PlanningSession, at time.Time) (bool, error) {
	if (session.Platform == "") != (session.ID == "") {
		return false, errors.New("activating Factory attempt: session platform and ID must both be set")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'active', session_platform = ?, session_id = ?, started_at = ?, updated_at = ?
		WHERE id = ? AND phase = 'prepared'`, session.Platform, session.ID, at.UnixMilli(), at.UnixMilli(), id)
	return factoryAttemptChanged(result, err, "activating Factory attempt")
}

func (d *DB) StopFactoryAttempt(ctx context.Context, id string, at time.Time) (bool, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'stopping', updated_at = ?
		WHERE id = ? AND phase = 'active' AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1'
		AND NOT EXISTS (SELECT 1 FROM factory_recovery_gate WHERE attempt_id = ? AND resolution = 'open')
		AND NOT EXISTS (SELECT 1 FROM factory_authority_escalation_gate WHERE attempt_id = ? AND resolution NOT IN ('approve', 'reject'))`, at.UnixMilli(), id, id, id)
	if err != nil {
		return false, fmt.Errorf("stopping Factory attempt: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 1 {
		return true, nil
	}
	var phase string
	if err := d.db.QueryRowContext(ctx, `SELECT phase FROM factory_attempt WHERE id = ?`, id).Scan(&phase); err != nil {
		return false, err
	}
	return phase == string(model.FactoryAttemptStopping), nil
}

func (d *DB) SetFactoryAttemptDeliveryTarget(ctx context.Context, id, remoteType, host, repo string, at time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var savedType, savedHost, savedRepo string
	err = tx.QueryRowContext(ctx, `SELECT json_extract(frozen_policy_json, '$.deliveryRemoteType'),
		json_extract(frozen_policy_json, '$.deliveryRemoteHost'), json_extract(frozen_policy_json, '$.deliveryRemoteRepo')
		FROM factory_attempt WHERE epic_id = (SELECT epic_id FROM factory_attempt WHERE id = ?)
		AND json_extract(frozen_policy_json, '$.deliveryRemoteRepo') <> '' ORDER BY sequence LIMIT 1`, id).Scan(&savedType, &savedHost, &savedRepo)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if savedRepo != "" {
		remoteType, host, repo = savedType, savedHost, savedRepo
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET frozen_policy_json = json_set(frozen_policy_json,
		'$.deliveryRemoteType', ?, '$.deliveryRemoteHost', ?, '$.deliveryRemoteRepo', ?), updated_at = ?
		WHERE id = ? AND phase = 'prepared'`, remoteType, host, repo, at.UnixMilli(), id)
	changed, err := factoryAttemptChanged(result, err, "setting Factory delivery target")
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("factory attempt is not prepared")
	}
	return tx.Commit()
}

// CompleteFactoryAttempt conditionally records immutable successful evidence.
func (d *DB) CompleteFactoryAttempt(ctx context.Context, id string, result model.FactoryAttemptResult, at time.Time) (bool, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("encoding Factory attempt result: %w", err)
	}
	updated, err := d.db.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'terminal', terminal_outcome = 'succeeded', result_json = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND phase = 'active'`, string(resultJSON), at.UnixMilli(), at.UnixMilli(), id)
	return factoryAttemptChanged(updated, err, "completing Factory attempt")
}

// CompleteFactoryImplementationAttempt records success and closes its work item atomically.
func (d *DB) CompleteFactoryImplementationAttempt(ctx context.Context, id, agentToken string, result model.FactoryAttemptResult, at time.Time) (bool, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return false, fmt.Errorf("encoding Factory attempt result: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if result.PRURL != "" {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT json_extract(result_json, '$.prUrl') FROM factory_attempt
			WHERE epic_id = (SELECT epic_id FROM factory_attempt WHERE id = ?) AND terminal_outcome = 'succeeded'
			AND json_extract(result_json, '$.prUrl') <> '' LIMIT 1`, id).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if existing != "" && existing != result.PRURL {
			return false, errors.New("factory epic already uses a different pull request")
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'terminal', terminal_outcome = 'succeeded', result_json = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND phase = 'stopping' AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1'
		AND EXISTS (SELECT 1 FROM factory_external_mapping WHERE system = 'factory' AND external_kind = 'attempt_token' AND external_id = ? AND entity_kind = 'attempt' AND entity_id = factory_attempt.id)
		AND NOT EXISTS (SELECT 1 FROM factory_recovery_gate WHERE attempt_id = ? AND resolution = 'open')
		AND NOT EXISTS (SELECT 1 FROM factory_authority_escalation_gate WHERE attempt_id = ? AND resolution NOT IN ('approve', 'reject'))`, string(resultJSON), at.UnixMilli(), at.UnixMilli(), id, agentToken, id, id)
	changed, err := factoryAttemptChanged(updated, err, "completing Factory implementation attempt")
	if err != nil || !changed {
		return changed, err
	}
	issue, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = ''
		WHERE id = (SELECT work_item_id FROM factory_attempt WHERE id = ?) AND status = 'in_progress'`, id)
	if err != nil {
		return false, err
	}
	n, err := issue.RowsAffected()
	if err != nil || n != 1 {
		return false, errors.New("completing Factory implementation issue failed")
	}
	now := at.UnixMilli()
	// The agent's final response lands after complete_attempt; keep that tail
	// activity from immediately auto-unarchiving the completed session.
	if _, err := tx.ExecContext(ctx, `INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
		SELECT session_platform, session_id, 9223372036854775807, ? FROM factory_attempt WHERE id = ? AND session_id <> ''
		ON CONFLICT(platform, session_id) DO UPDATE SET session_time_updated = excluded.session_time_updated, archived_at = excluded.archived_at`, now, id); err != nil {
		return false, fmt.Errorf("archiving completed Factory session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unarchived_entity WHERE kind = 'session' AND remote_id = 'local' AND entity_key = (SELECT session_platform || char(0) || session_id FROM factory_attempt WHERE id = ?)`, id); err != nil {
		return false, fmt.Errorf("clearing completed Factory session unarchive: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) FactoryEpicPRURL(ctx context.Context, epicID string) (string, error) {
	var prURL string
	err := d.db.QueryRowContext(ctx, `SELECT json_extract(result_json, '$.prUrl') FROM factory_attempt
		WHERE epic_id = ? AND terminal_outcome = 'succeeded' AND json_extract(result_json, '$.prUrl') <> '' LIMIT 1`, epicID).Scan(&prURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return prURL, err
}

func (d *DB) ValidateFactoryAttemptToken(ctx context.Context, attemptID, token string) (bool, error) {
	var found int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_external_mapping WHERE system = 'factory' AND external_kind = 'attempt_token' AND external_id = ? AND entity_kind = 'attempt' AND entity_id = ?`, token, attemptID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// factoryRetryBackoff is the wait before each automatic relaunch of failed
// implementation work; its length is the retry budget.
var factoryRetryBackoff = [...]time.Duration{time.Second, 30 * time.Second, 5 * time.Minute}

// FailFactoryAttempt conditionally terminates preparation or execution.
func (d *DB) FailFactoryAttempt(ctx context.Context, id string, failure model.FactoryAttemptFailure, at time.Time) (bool, error) {
	if failure.Type == "" {
		return false, errors.New("failing Factory attempt: failure type is required")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'terminal', terminal_outcome = 'failed', failure_type = ?, failure_message = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND phase IN ('prepared', 'active', 'stopping')`, failure.Type, failure.Message, at.UnixMilli(), at.UnixMilli(), id)
	changed, err := factoryAttemptChanged(result, err, "failing Factory attempt")
	if err != nil || !changed {
		return changed, err
	}
	if failure.Type == "launch_failed" || failure.Type == "activation_failed" || failure.Type == "prompt_failed" || failure.Type == "interrupted_startup" || failure.Type == "interrupted_runtime" || failure.Type == "handoff_finalize_failed" || failure.Type == "delivery_migration" {
		// ponytail: fixed 1s/30s/5m backoff so a restarting opencode instance
		// doesn't burn the whole budget in two seconds; ReopenFactoryIssue is
		// the human escape hatch once it is exhausted.
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET
			status = CASE WHEN retry_attempts < ? THEN 'retry_wait' ELSE 'closed' END,
			outcome = CASE WHEN retry_attempts < ? THEN '' ELSE 'failed' END,
			outcome_reason = ?, retry_at = CASE retry_attempts WHEN 0 THEN ? WHEN 1 THEN ? WHEN 2 THEN ? ELSE 0 END,
			retry_attempts = retry_attempts + 1
			WHERE id = (SELECT work_item_id FROM factory_attempt WHERE id = ? AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1') AND status = 'in_progress'`,
			len(factoryRetryBackoff), len(factoryRetryBackoff), failure.Message, at.Add(factoryRetryBackoff[0]).UnixMilli(), at.Add(factoryRetryBackoff[1]).UnixMilli(), at.Add(factoryRetryBackoff[2]).UnixMilli(), id); err != nil {
			return false, err
		}
	}
	if failure.Type == "prompt_failed" {
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open'
			WHERE id = (SELECT work_item_id FROM factory_attempt WHERE id = ? AND json_extract(frozen_policy_json, '$.profile') = 'factory-plan/v1') AND status = 'in_progress'`, id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CreateFactoryRecoveryGate records an agent-requested human decision without
// terminating its live implementation session.
func (d *DB) CreateFactoryRecoveryGate(ctx context.Context, attemptID, question, reason string, choices []string, at time.Time) (model.RecoveryGate, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.RecoveryGate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var existingID, resolution string
	err = tx.QueryRowContext(ctx, `SELECT issue_id, resolution FROM factory_recovery_gate WHERE attempt_id = ?`, attemptID).Scan(&existingID, &resolution)
	if err == nil {
		if resolution != "open" {
			return model.RecoveryGate{}, errors.New("factory recovery gate is already resolved")
		}
		return loadFactoryRecoveryGate(ctx, tx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.RecoveryGate{}, err
	}
	var parentID string
	if err := tx.QueryRowContext(ctx, `SELECT h.parent_issue_id FROM factory_attempt a JOIN factory_issue i ON i.id = a.work_item_id JOIN factory_issue_hierarchy h ON h.child_issue_id = i.id WHERE a.id = ? AND a.phase = 'active' AND json_extract(a.frozen_policy_json, '$.profile') = 'factory-implement/v1'`, attemptID).Scan(&parentID); err != nil {
		return model.RecoveryGate{}, errors.New("factory implementation attempt is unavailable for recovery")
	}
	gateID, err := factoryChildID(ctx, tx, parentID)
	if err != nil {
		return model.RecoveryGate{}, err
	}
	if choices == nil {
		choices = []string{}
	}
	choicesJSON, err := json.Marshal(choices)
	if err != nil {
		return model.RecoveryGate{}, err
	}
	now := at.UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) SELECT ?, epic_id, 'gate', 'Recovery gate', ?, 'open', ? FROM factory_attempt WHERE id = ?`, gateID, question, now, attemptID); err != nil {
		return model.RecoveryGate{}, err
	}
	index, err := factoryChildIndex(gateID)
	if err != nil {
		return model.RecoveryGate{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'reference')`, parentID, gateID, index); err != nil {
		return model.RecoveryGate{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_recovery_gate (issue_id, epic_id, attempt_id, work_item_id, question, reason, choices_json, created_at) SELECT ?, epic_id, id, work_item_id, ?, ?, ?, ? FROM factory_attempt WHERE id = ?`, gateID, question, reason, string(choicesJSON), now, attemptID); err != nil {
		return model.RecoveryGate{}, err
	}
	details, _ := json.Marshal(map[string]any{"question": question, "reason": reason, "choices": choices})
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) SELECT epic_id, work_item_id, id, 'agent', 'recovery.requested', ?, ? FROM factory_attempt WHERE id = ?`, string(details), now, attemptID); err != nil {
		return model.RecoveryGate{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.RecoveryGate{}, err
	}
	return d.getFactoryRecoveryGate(ctx, gateID)
}

func (d *DB) IsFactoryAttemptRecoveryPaused(ctx context.Context, attemptID string) (bool, error) {
	var found int
	err := d.db.QueryRowContext(ctx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM factory_recovery_gate WHERE attempt_id = ? AND resolution = 'open') OR EXISTS (SELECT 1 FROM factory_authority_escalation_gate WHERE attempt_id = ? AND resolution NOT IN ('approve', 'reject'))`, attemptID, attemptID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// CreateFactoryAuthorityEscalationGate persists an out-of-profile request.
// The unique request key makes duplicate platform events idempotent.
func (d *DB) IsFactoryImplementationSession(ctx context.Context, session string) (bool, error) {
	var found int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_attempt WHERE session_id = ? AND phase = 'active' AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1'`, session).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (d *DB) CreateFactoryAuthorityEscalationGate(ctx context.Context, session, requestID, permission, target string, at time.Time) (model.AuthorityEscalationGate, bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var attemptID, parentID string
	err = tx.QueryRowContext(ctx, `SELECT a.id, h.parent_issue_id FROM factory_attempt a JOIN factory_issue i ON i.id = a.work_item_id JOIN factory_issue_hierarchy h ON h.child_issue_id = i.id WHERE a.session_id = ? AND a.phase = 'active' AND json_extract(a.frozen_policy_json, '$.profile') = 'factory-implement/v1'`, session).Scan(&attemptID, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AuthorityEscalationGate{}, false, nil
	}
	if err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	if permission != "external_directory" {
		return model.AuthorityEscalationGate{}, false, nil
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT issue_id FROM factory_authority_escalation_gate WHERE attempt_id = ? AND request_id = ?`, attemptID, requestID).Scan(&existing)
	if err == nil {
		gate, err := loadFactoryAuthorityEscalationGate(ctx, tx, existing)
		return gate, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.AuthorityEscalationGate{}, false, err
	}
	gateID, err := factoryChildID(ctx, tx, parentID)
	if err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	index, err := factoryChildIndex(gateID)
	if err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	now := at.UnixMilli()
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) SELECT ?, epic_id, 'gate', 'Authority escalation gate', ?, 'open', ? FROM factory_attempt WHERE id = ?`, gateID, permission+": "+target, now, attemptID); err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'reference')`, parentID, gateID, index); err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_authority_escalation_gate (issue_id, epic_id, attempt_id, work_item_id, request_id, permission, target, created_at) SELECT ?, epic_id, id, work_item_id, ?, ?, ?, ? FROM factory_attempt WHERE id = ?`, gateID, requestID, permission, target, now, attemptID); err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	details, _ := json.Marshal(map[string]string{"requestId": requestID, "permission": permission, "target": target})
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) SELECT epic_id, work_item_id, id, 'agent', 'authority.requested', ?, ? FROM factory_attempt WHERE id = ?`, string(details), now, attemptID); err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return model.AuthorityEscalationGate{}, false, err
	}
	gate, err := d.getFactoryAuthorityEscalationGate(ctx, gateID)
	return gate, true, err
}

func (d *DB) ResolveFactoryAuthorityEscalationGate(ctx context.Context, gateID, action string, at time.Time) (model.AuthorityEscalationGate, model.FactoryAttempt, error) {
	if action != "approve" && action != "reject" {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, errors.New("invalid authority escalation action")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := loadFactoryAuthorityEscalationGate(ctx, tx, gateID)
	if err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, fmt.Errorf("reading authority escalation gate: %w", err)
	}
	if gate.Resolution != "open" {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, errors.New("authority escalation gate is unavailable")
	}
	attempt, err := scanFactoryAttempt(tx.QueryRowContext(ctx, `SELECT `+factoryAttemptColumns+` FROM factory_attempt WHERE id = ? AND phase = 'active'`, gate.AttemptID))
	if err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, errors.New("authority escalation attempt is unavailable")
	}
	now := at.UnixMilli()
	gate.Resolution = action + "_pending"
	if _, err = tx.ExecContext(ctx, `UPDATE factory_authority_escalation_gate SET resolution = ?, resolved_at = ? WHERE issue_id = ?`, gate.Resolution, now, gateID); err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) VALUES (?, ?, ?, 'user', ?, '{}', ?)`, gate.EpicID, gate.WorkID, gate.AttemptID, "authority."+action, now); err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.AuthorityEscalationGate{}, model.FactoryAttempt{}, err
	}
	return gate, attempt, nil
}

func (d *DB) CompleteFactoryAuthorityEscalationGate(ctx context.Context, gateID, action string, at time.Time) (model.AuthorityEscalationGate, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AuthorityEscalationGate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE factory_authority_escalation_gate SET resolution = ?, resolved_at = ? WHERE issue_id = ? AND resolution = ?`, action, at.UnixMilli(), gateID, action+"_pending")
	changed, err := factoryAttemptChanged(result, err, "completing authority escalation delivery")
	if err != nil {
		return model.AuthorityEscalationGate{}, err
	}
	if !changed {
		return model.AuthorityEscalationGate{}, errors.New("authority escalation gate is unavailable")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = ?, outcome_reason = ? WHERE id = ?`, map[bool]string{true: "succeeded", false: "cancelled"}[action == "approve"], action, gateID); err != nil {
		return model.AuthorityEscalationGate{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AuthorityEscalationGate{}, err
	}
	return d.getFactoryAuthorityEscalationGate(ctx, gateID)
}

func (d *DB) getFactoryAuthorityEscalationGate(ctx context.Context, gateID string) (model.AuthorityEscalationGate, error) {
	return loadFactoryAuthorityEscalationGate(ctx, d.db, gateID)
}
func (d *DB) GetFactoryAuthorityEscalationGate(ctx context.Context, gateID string) (model.AuthorityEscalationGate, bool, error) {
	gate, err := d.getFactoryAuthorityEscalationGate(ctx, gateID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AuthorityEscalationGate{}, false, nil
	}
	return gate, err == nil, err
}
func loadFactoryAuthorityEscalationGate(ctx context.Context, scanner interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, gateID string) (model.AuthorityEscalationGate, error) {
	var gate model.AuthorityEscalationGate
	err := scanner.QueryRowContext(ctx, `SELECT issue_id, epic_id, attempt_id, work_item_id, request_id, permission, target, resolution FROM factory_authority_escalation_gate WHERE issue_id = ?`, gateID).Scan(&gate.IssueID, &gate.EpicID, &gate.AttemptID, &gate.WorkID, &gate.RequestID, &gate.Permission, &gate.Target, &gate.Resolution)
	return gate, err
}

func (d *DB) GetFactoryRecoveryGate(ctx context.Context, gateID string) (model.RecoveryGate, bool, error) {
	gate, err := d.getFactoryRecoveryGate(ctx, gateID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.RecoveryGate{}, false, nil
	}
	return gate, err == nil, err
}

func (d *DB) ResolveFactoryRecoveryGate(ctx context.Context, gateID, action, response string, at time.Time) (model.RecoveryGate, model.FactoryAttempt, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := loadFactoryRecoveryGate(ctx, tx, gateID)
	if err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, fmt.Errorf("reading Factory recovery gate: %w", err)
	}
	if gate.Resolution != "open" {
		return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("factory recovery gate is unavailable")
	}
	attempt, err := scanFactoryAttempt(tx.QueryRowContext(ctx, `SELECT `+factoryAttemptColumns+` FROM factory_attempt WHERE id = ? AND phase = 'active'`, gate.AttemptID))
	if err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("factory recovery attempt is unavailable")
	}
	now := at.UnixMilli()
	switch action {
	case "resume":
		var globalLimit, projectLimit, global, project int
		globalLimit, projectLimit = 10, 4
		_ = tx.QueryRowContext(ctx, `SELECT global_capacity, project_capacity FROM factory_capacity_policy WHERE id = 1`).Scan(&globalLimit, &projectLimit)
		_ = tx.QueryRowContext(ctx, `SELECT capacity FROM factory_project_capacity_override WHERE project_path = ?`, attempt.FrozenPolicy.Repository).Scan(&projectLimit)
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN json_extract(frozen_policy_json, '$.repository') = ? THEN 1 ELSE 0 END), 0) FROM factory_attempt a WHERE phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1' AND (a.id = ? OR (NOT EXISTS (SELECT 1 FROM factory_recovery_gate r WHERE r.attempt_id = a.id AND r.resolution = 'open') AND NOT EXISTS (SELECT 1 FROM factory_authority_escalation_gate g WHERE g.attempt_id = a.id AND g.resolution NOT IN ('approve', 'reject'))))`, attempt.FrozenPolicy.Repository, attempt.ID).Scan(&global, &project); err != nil {
			return model.RecoveryGate{}, model.FactoryAttempt{}, err
		}
		if global > globalLimit || project > projectLimit {
			return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("factory implementation capacity is full")
		}
	case "retry", "cancel":
		if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'cancelled', finished_at = ?, updated_at = ? WHERE id = ? AND phase = 'active'`, now, now, attempt.ID); err != nil {
			return model.RecoveryGate{}, model.FactoryAttempt{}, err
		}
		status, outcome, reason := "open", "", ""
		if action == "cancel" {
			status, outcome, reason = "closed", "cancelled", "recovery_cancelled"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = ?, outcome = ?, outcome_reason = ? WHERE id = ?`, status, outcome, reason, gate.WorkID); err != nil {
			return model.RecoveryGate{}, model.FactoryAttempt{}, err
		}
	default:
		return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("invalid recovery gate action")
	}
	gate.Resolution, gate.Response = action, response
	gateOutcome := "succeeded"
	if action == "cancel" {
		gateOutcome = "cancelled"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_recovery_gate SET response = ?, resolution = ?, resolved_at = ? WHERE issue_id = ?`, response, action, now, gateID); err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = ?, outcome_reason = ? WHERE id = ?`, gateOutcome, action, gateID); err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	details, _ := json.Marshal(map[string]string{"response": response})
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) VALUES (?, ?, ?, 'user', ?, ?, ?)`, gate.EpicID, gate.WorkID, gate.AttemptID, "recovery."+action, string(details), now); err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	return gate, attempt, nil
}

func (d *DB) getFactoryRecoveryGate(ctx context.Context, gateID string) (model.RecoveryGate, error) {
	return loadFactoryRecoveryGate(ctx, d.db, gateID)
}

func loadFactoryRecoveryGate(ctx context.Context, scanner interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, gateID string) (model.RecoveryGate, error) {
	var gate model.RecoveryGate
	var choices string
	err := scanner.QueryRowContext(ctx, `SELECT issue_id, epic_id, attempt_id, work_item_id, question, reason, choices_json, response, resolution FROM factory_recovery_gate WHERE issue_id = ?`, gateID).Scan(&gate.IssueID, &gate.EpicID, &gate.AttemptID, &gate.WorkID, &gate.Question, &gate.Reason, &choices, &gate.Response, &gate.Resolution)
	if err != nil {
		return model.RecoveryGate{}, err
	}
	if err := json.Unmarshal([]byte(choices), &gate.Choices); err != nil {
		return model.RecoveryGate{}, err
	}
	return gate, nil
}

type factoryAttemptScanner interface {
	Scan(...any) error
}

func scanFactoryAttempt(scanner factoryAttemptScanner) (model.FactoryAttempt, error) {
	var attempt model.FactoryAttempt
	var retryOf sql.NullString
	var policyJSON, resultJSON string
	if err := scanner.Scan(
		&attempt.ID, &attempt.EpicID, &attempt.WorkID, &attempt.Sequence, &attempt.Phase, &attempt.Outcome,
		&attempt.Session.Platform, &attempt.Session.ID, &retryOf, &attempt.RetryAt, &policyJSON,
		&resultJSON, &attempt.Failure.Type, &attempt.Failure.Message, &attempt.CreatedAt, &attempt.UpdatedAt,
		&attempt.StartedAt, &attempt.FinishedAt,
	); err != nil {
		return model.FactoryAttempt{}, err
	}
	attempt.RetryOfAttemptID = retryOf.String
	if err := json.Unmarshal([]byte(policyJSON), &attempt.FrozenPolicy); err != nil {
		return model.FactoryAttempt{}, fmt.Errorf("decoding frozen policy: %w", err)
	}
	if resultJSON != "" {
		attempt.Result = &model.FactoryAttemptResult{}
		if err := json.Unmarshal([]byte(resultJSON), attempt.Result); err != nil {
			return model.FactoryAttempt{}, fmt.Errorf("decoding result: %w", err)
		}
	}
	return attempt, nil
}

func factoryAttemptChanged(result sql.Result, err error, action string) (bool, error) {
	if err != nil {
		return false, fmt.Errorf("%s: %w", action, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s: %w", action, err)
	}
	return changed != 0, nil
}

// HasFactoryLocalExecutionAck reports whether the operator has acknowledged
// one host, repository, and permission profile tuple.
func (d *DB) HasFactoryLocalExecutionAck(ctx context.Context, hostID, repoRoot, profileID, profileVersion string) (bool, error) {
	var acknowledged int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_local_execution_ack
		WHERE host_id = ? AND repo_root = ? AND profile_id = ? AND profile_version = ?`,
		hostID, repoRoot, profileID, profileVersion).Scan(&acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking Factory local execution acknowledgement: %w", err)
	}
	return true, nil
}

// UpsertFactoryLocalExecutionAck records the operator's acknowledgement for
// one host, repository, and permission profile tuple.
func (d *DB) UpsertFactoryLocalExecutionAck(ctx context.Context, hostID, repoRoot, profileID, profileVersion, actor string, at time.Time) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO factory_local_execution_ack
			(host_id, repo_root, profile_id, profile_version, acknowledged_by, acknowledged_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, repo_root, profile_id, profile_version) DO UPDATE SET
			acknowledged_by = excluded.acknowledged_by,
			acknowledged_at = excluded.acknowledged_at
	`, hostID, repoRoot, profileID, profileVersion, actor, at.UnixMilli())
	if err != nil {
		return fmt.Errorf("upserting Factory local execution acknowledgement: %w", err)
	}
	return nil
}

// GetFactoryPlanningSession returns the durable session bound to one Planning Work item.
func (d *DB) GetFactoryPlanningSession(ctx context.Context, workID string) (model.PlanningSession, bool, error) {
	var session model.PlanningSession
	var metadata string
	err := d.db.QueryRowContext(ctx, `
		SELECT external_id, metadata_json
		FROM factory_external_mapping
		WHERE system = 'session' AND external_kind = 'planning' AND entity_kind = 'planning_work' AND entity_id = ?
	`, workID).Scan(&session.ID, &metadata)
	if err == sql.ErrNoRows {
		return model.PlanningSession{}, false, nil
	}
	if err != nil {
		return model.PlanningSession{}, false, fmt.Errorf("getting Factory Planning Session: %w", err)
	}
	var stored struct {
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal([]byte(metadata), &stored); err != nil || stored.Platform == "" {
		return model.PlanningSession{}, false, errors.New("getting Factory Planning Session: invalid metadata")
	}
	session.Platform = stored.Platform
	return session, true, nil
}

// PutFactoryPlanningSession binds one Planning Work item to exactly one session.
func (d *DB) PutFactoryPlanningSession(ctx context.Context, epicID, workID string, session model.PlanningSession) error {
	metadata, err := json.Marshal(map[string]string{"epicId": epicID, "platform": session.Platform})
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_external_mapping
			(system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('session', 'planning', ?, 'planning_work', ?, ?, ?)
		ON CONFLICT(system, external_kind, entity_kind, entity_id) DO NOTHING
	`, session.ID, workID, string(metadata), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("putting Factory Planning Session: %w", err)
	}
	stored, ok, err := d.GetFactoryPlanningSession(ctx, workID)
	if err != nil {
		return err
	}
	if !ok || stored != session {
		return errors.New("putting Factory Planning Session: Planning Work is already bound to another session")
	}
	return nil
}

// DeleteFactoryPlanningSession clears a dead Planning Session binding.
func (d *DB) DeleteFactoryPlanningSession(ctx context.Context, workID string) error {
	_, err := d.db.ExecContext(ctx, `
		DELETE FROM factory_external_mapping
		WHERE system = 'session' AND external_kind = 'planning' AND entity_kind = 'planning_work' AND entity_id = ?
	`, workID)
	if err != nil {
		return fmt.Errorf("deleting Factory Planning Session: %w", err)
	}
	return nil
}

// PutFactoryPlanningSessionCleanup records a session that must be disposed
// before Factory can admit another mutation.
func (d *DB) PutFactoryPlanningSessionCleanup(ctx context.Context, epicID, workID string, session model.PlanningSession) error {
	metadata, err := json.Marshal(map[string]string{"epicId": epicID, "platform": session.Platform})
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_external_mapping
			(system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('session', 'planning_cleanup', ?, 'planning_work', ?, ?, ?)
		ON CONFLICT(system, external_kind, entity_kind, entity_id) DO UPDATE SET
			external_id = excluded.external_id, metadata_json = excluded.metadata_json
	`, session.ID, workID, string(metadata), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("putting Factory Planning Session cleanup: %w", err)
	}
	return nil
}

// ListFactoryPlanningSessionCleanups returns every pending restricted-session cleanup.
func (d *DB) ListFactoryPlanningSessionCleanups(ctx context.Context) (map[string]model.PlanningSession, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT entity_id, external_id, metadata_json
		FROM factory_external_mapping
		WHERE system = 'session' AND external_kind = 'planning_cleanup' AND entity_kind = 'planning_work'
	`)
	if err != nil {
		return nil, fmt.Errorf("listing Factory Planning Session cleanups: %w", err)
	}
	defer rows.Close()
	cleanups := map[string]model.PlanningSession{}
	for rows.Next() {
		var workID, metadata string
		var session model.PlanningSession
		if err := rows.Scan(&workID, &session.ID, &metadata); err != nil {
			return nil, fmt.Errorf("listing Factory Planning Session cleanups: %w", err)
		}
		var stored struct {
			Platform string `json:"platform"`
		}
		if err := json.Unmarshal([]byte(metadata), &stored); err != nil || stored.Platform == "" {
			return nil, errors.New("listing Factory Planning Session cleanups: invalid metadata")
		}
		session.Platform = stored.Platform
		cleanups[workID] = session
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing Factory Planning Session cleanups: %w", err)
	}
	return cleanups, nil
}

// DeleteFactoryPlanningSessionCleanup clears a completed cleanup intent.
func (d *DB) DeleteFactoryPlanningSessionCleanup(ctx context.Context, workID string) error {
	_, err := d.db.ExecContext(ctx, `
		DELETE FROM factory_external_mapping
		WHERE system = 'session' AND external_kind = 'planning_cleanup' AND entity_kind = 'planning_work' AND entity_id = ?
	`, workID)
	if err != nil {
		return fmt.Errorf("deleting Factory Planning Session cleanup: %w", err)
	}
	return nil
}

// AppendFactoryAudit records an immutable Factory decision or graph transition.
func (d *DB) AppendFactoryAudit(ctx context.Context, record model.AuditRecord) error {
	details, err := json.Marshal(record.Details)
	if err != nil {
		return fmt.Errorf("encoding Factory audit: %w", err)
	}
	var attemptID any
	if record.AttemptID != "" {
		attemptID = record.AttemptID
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, record.EpicID, record.WorkID, attemptID, record.Actor, record.Action, string(details), record.At.UnixMilli())
	if err != nil {
		return fmt.Errorf("appending Factory audit: %w", err)
	}
	return nil
}

// AppendFactoryAuditOnce records a transition once across retries and recovery.
func (d *DB) AppendFactoryAuditOnce(ctx context.Context, record model.AuditRecord) error {
	details, err := json.Marshal(record.Details)
	if err != nil {
		return fmt.Errorf("encoding Factory audit: %w", err)
	}
	var attemptID any
	if record.AttemptID != "" {
		attemptID = record.AttemptID
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM factory_audit_record
			WHERE epic_id = ? AND work_item_id = ? AND attempt_id IS ? AND actor = ? AND action = ? AND details_json = ?
		)
	`, record.EpicID, record.WorkID, attemptID, record.Actor, record.Action, string(details), record.At.UnixMilli(), record.EpicID, record.WorkID, attemptID, record.Actor, record.Action, string(details))
	if err != nil {
		return fmt.Errorf("appending Factory audit once: %w", err)
	}
	return nil
}
