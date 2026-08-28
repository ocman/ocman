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

// FailFactoryAttempt conditionally terminates preparation or execution.
func (d *DB) FailFactoryAttempt(ctx context.Context, id string, failure model.FactoryAttemptFailure, at time.Time) (bool, error) {
	if failure.Type == "" {
		return false, errors.New("failing Factory attempt: failure type is required")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE factory_attempt
		SET phase = 'terminal', terminal_outcome = 'failed', failure_type = ?, failure_message = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND phase IN ('prepared', 'active', 'stopping')`, failure.Type, failure.Message, at.UnixMilli(), at.UnixMilli(), id)
	return factoryAttemptChanged(result, err, "failing Factory attempt")
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

func (d *DB) ListFactoryFormulas(ctx context.Context) ([]model.Formula, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, source, current_revision, archived_at, created_at, updated_at FROM factory_formula ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing Factory formulas: %w", err)
	}
	defer rows.Close()
	var formulas []model.Formula
	for rows.Next() {
		var formula model.Formula
		if err := rows.Scan(&formula.ID, &formula.Name, &formula.Source, &formula.CurrentRevision, &formula.ArchivedAt, &formula.CreatedAt, &formula.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning Factory formula: %w", err)
		}
		formulas = append(formulas, formula)
	}
	return formulas, rows.Err()
}

func (d *DB) GetFactoryFormulaRevision(ctx context.Context, formulaID string, revision int) (model.Formula, model.FormulaRevision, error) {
	var formula model.Formula
	err := d.db.QueryRowContext(ctx, `SELECT id, name, source, current_revision, archived_at, created_at, updated_at FROM factory_formula WHERE id = ?`, formulaID).
		Scan(&formula.ID, &formula.Name, &formula.Source, &formula.CurrentRevision, &formula.ArchivedAt, &formula.CreatedAt, &formula.UpdatedAt)
	if err != nil {
		return model.Formula{}, model.FormulaRevision{}, err
	}
	if revision == 0 {
		revision = formula.CurrentRevision
	}
	var saved model.FormulaRevision
	err = d.db.QueryRowContext(ctx, `SELECT formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at
		FROM factory_formula_revision WHERE formula_id = ? AND revision = ?`, formulaID, revision).
		Scan(&saved.FormulaID, &saved.Revision, &saved.SchemaVersion, &saved.DefinitionYAML, &saved.ContentHash, &saved.ValidationJSON, &saved.CreatedAt)
	return formula, saved, err
}

func (d *DB) SaveFactoryFormulaRevision(ctx context.Context, id, name, definitionYAML, contentHash, validationJSON string, schemaVersion int, at time.Time) (model.FormulaRevision, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.FormulaRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var source string
	var current int
	err = tx.QueryRowContext(ctx, `SELECT source, current_revision FROM factory_formula WHERE id = ?`, id).Scan(&source, &current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		current = 0
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_formula (id, name, source, current_revision, created_at, updated_at, archived_at) VALUES (?, ?, 'custom', 1, ?, ?, 0)`, id, name, at.UnixMilli(), at.UnixMilli())
	case err != nil:
		return model.FormulaRevision{}, err
	case source != "custom":
		return model.FormulaRevision{}, errors.New("built-in Factory formula is immutable")
	default:
		var existing model.FormulaRevision
		err = tx.QueryRowContext(ctx, `SELECT formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at FROM factory_formula_revision WHERE formula_id = ? AND content_hash = ?`, id, contentHash).
			Scan(&existing.FormulaID, &existing.Revision, &existing.SchemaVersion, &existing.DefinitionYAML, &existing.ContentHash, &existing.ValidationJSON, &existing.CreatedAt)
		if err == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE factory_formula SET name = ?, archived_at = 0, updated_at = ? WHERE id = ?`, name, at.UnixMilli(), id); err != nil {
				return model.FormulaRevision{}, err
			}
			if err := tx.Commit(); err != nil {
				return model.FormulaRevision{}, err
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.FormulaRevision{}, err
		}
		current++
		_, err = tx.ExecContext(ctx, `UPDATE factory_formula SET name = ?, current_revision = ?, archived_at = 0, updated_at = ? WHERE id = ?`, name, current, at.UnixMilli(), id)
	}
	if err != nil {
		return model.FormulaRevision{}, err
	}
	if current == 0 {
		current = 1
	}
	saved := model.FormulaRevision{FormulaID: id, Revision: current, SchemaVersion: schemaVersion, DefinitionYAML: definitionYAML, ContentHash: contentHash, ValidationJSON: validationJSON, CreatedAt: at.UnixMilli()}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_formula_revision (formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, current, schemaVersion, definitionYAML, contentHash, validationJSON, saved.CreatedAt)
	if err != nil {
		return model.FormulaRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.FormulaRevision{}, err
	}
	return saved, nil
}

func (d *DB) ArchiveFactoryFormula(ctx context.Context, id string, at time.Time) (bool, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_formula SET archived_at = ?, updated_at = ? WHERE id = ? AND source = 'custom'`, at.UnixMilli(), at.UnixMilli(), id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed != 0, err
}

func (d *DB) DeleteFactoryFormula(ctx context.Context, id string) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM factory_formula_revision WHERE formula_id = ? AND EXISTS (SELECT 1 FROM factory_formula WHERE id = ? AND source = 'custom')`, id, id); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM factory_formula WHERE id = ? AND source = 'custom'`, id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed != 0, nil
}
