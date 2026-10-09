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

// Recovery alone does not release a workspace. The service must stop the
// writer and validate its clean, pushed checkpoint before recording this marker.
const factoryRecoveryYieldedSQL = `a.phase = 'active' AND EXISTS (
	SELECT 1 FROM factory_recovery_gate r WHERE r.attempt_id = a.id AND r.resolution = 'open'
	AND json_extract(CASE WHEN json_valid(a.result_json) THEN a.result_json ELSE '{}' END, '$.summary') = 'Recovery workspace checkpoint ' || r.issue_id
	AND json_extract(CASE WHEN json_valid(a.result_json) THEN a.result_json ELSE '{}' END, '$.commitSha') <> '')`

func (d *DB) RecordFactoryRecoveryCheckpoint(ctx context.Context, gateID string, result model.FactoryAttemptResult, at time.Time) error {
	if result.CommitSHA == "" || result.Branch == "" {
		return errors.New("recovery handoff requires a verified checkpoint")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := loadFactoryRecoveryGate(ctx, tx, gateID)
	if err != nil {
		return err
	}
	if gate.Resolution != "open" {
		return errors.New("factory recovery gate is unavailable")
	}
	attempt, err := scanFactoryAttempt(tx.QueryRowContext(ctx, `SELECT `+factoryAttemptColumns+` FROM factory_attempt WHERE id = ? AND phase = 'active'`, gate.AttemptID))
	if err != nil {
		return errors.New("factory recovery attempt is unavailable")
	}
	if result.Branch != attempt.FrozenPolicy.Branch {
		return errors.New("recovery checkpoint belongs to another branch")
	}
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_attempt a WHERE epic_id = ? AND id <> ? AND phase IN ('prepared','active','stopping') AND json_extract(frozen_policy_json,'$.profile') = 'factory-implement/v1' AND NOT (`+factoryRecoveryYieldedSQL+`)`, gate.EpicID, attempt.ID).Scan(&owners); err != nil {
		return err
	}
	if owners != 0 {
		return errors.New("factory Epic workspace is in use")
	}
	result.SchemaVersion, result.Summary = 2, "Recovery workspace checkpoint "+gateID
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET result_json = ?, updated_at = ? WHERE id = ?`, string(raw), at.UnixMilli(), attempt.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record(epic_id,work_item_id,attempt_id,actor,action,details_json,created_at) VALUES (?,?,?,'factory','recovery.checkpoint',?,?)`, gate.EpicID, gate.WorkID, attempt.ID, string(raw), at.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
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
	pending := action == "resume" && gate.Resolution == "resume_pending" && gate.Response == response
	if gate.Resolution != "open" && !pending {
		return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("factory recovery gate is unavailable")
	}
	attempt, err := scanFactoryAttempt(tx.QueryRowContext(ctx, `SELECT `+factoryAttemptColumns+` FROM factory_attempt WHERE id = ? AND phase = 'active'`, gate.AttemptID))
	if err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, errors.New("factory recovery attempt is unavailable")
	}
	now := at.UnixMilli()
	switch action {
	case "resume":
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT work_item_id FROM factory_attempt a WHERE epic_id = ? AND id <> ? AND phase IN ('prepared','active','stopping') AND json_extract(frozen_policy_json,'$.profile') = 'factory-implement/v1' AND NOT (`+factoryRecoveryYieldedSQL+`) ORDER BY created_at, id LIMIT 1`, attempt.EpicID, attempt.ID).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return model.RecoveryGate{}, model.FactoryAttempt{}, err
		}
		if err == nil {
			return model.RecoveryGate{}, model.FactoryAttempt{}, fmt.Errorf("%w by Issue %s. Resume after that Issue releases the workspace", model.ErrRecoveryWorkspaceBusy, owner)
		}
		if attempt.Result != nil && strings.HasPrefix(attempt.Result.Summary, "Recovery workspace checkpoint ") {
			head, err := latestFactoryCheckpoint(ctx, tx, attempt.EpicID, attempt.FrozenPolicy.Repository)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return model.RecoveryGate{}, model.FactoryAttempt{}, err
			}
			if err == nil {
				attempt.FrozenPolicy.CheckpointSHA = head
				policy, err := json.Marshal(attempt.FrozenPolicy)
				if err != nil {
					return model.RecoveryGate{}, model.FactoryAttempt{}, err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET frozen_policy_json = ?, updated_at = ? WHERE id = ?`, string(policy), now, attempt.ID); err != nil {
					return model.RecoveryGate{}, model.FactoryAttempt{}, err
				}
			}
		}
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
	if pending {
		return gate, attempt, nil
	}
	gate.Resolution, gate.Response = action, response
	if action == "resume" {
		gate.Resolution = "resume_pending"
	}
	gateOutcome := "succeeded"
	if action == "cancel" {
		gateOutcome = "cancelled"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_recovery_gate SET response = ?, resolution = ?, resolved_at = ? WHERE issue_id = ?`, response, gate.Resolution, now, gateID); err != nil {
		return model.RecoveryGate{}, model.FactoryAttempt{}, err
	}
	if action != "resume" {
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = ?, outcome_reason = ? WHERE id = ?`, gateOutcome, action, gateID); err != nil {
			return model.RecoveryGate{}, model.FactoryAttempt{}, err
		}
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
