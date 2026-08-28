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
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, record.EpicID, record.WorkID, record.Actor, record.Action, string(details), record.At.UnixMilli())
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
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at)
		SELECT ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM factory_audit_record
			WHERE epic_id = ? AND work_item_id = ? AND actor = ? AND action = ? AND details_json = ?
		)
	`, record.EpicID, record.WorkID, record.Actor, record.Action, string(details), record.At.UnixMilli(), record.EpicID, record.WorkID, record.Actor, record.Action, string(details))
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
