package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type FactoryFormula struct {
	ID              string
	Name            string
	Source          string
	CurrentRevision int
	ArchivedAt      int64
	CreatedAt       int64
	UpdatedAt       int64
}

type FactoryFormulaRevision struct {
	FormulaID      string
	Revision       int
	SchemaVersion  int
	DefinitionYAML string
	ContentHash    string
	ValidationJSON string
	CreatedAt      int64
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

func (d *DB) ListFactoryFormulas(ctx context.Context) ([]FactoryFormula, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, source, current_revision, archived_at, created_at, updated_at FROM factory_formula ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("listing Factory formulas: %w", err)
	}
	defer rows.Close()
	var formulas []FactoryFormula
	for rows.Next() {
		var formula FactoryFormula
		if err := rows.Scan(&formula.ID, &formula.Name, &formula.Source, &formula.CurrentRevision, &formula.ArchivedAt, &formula.CreatedAt, &formula.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning Factory formula: %w", err)
		}
		formulas = append(formulas, formula)
	}
	return formulas, rows.Err()
}

func (d *DB) GetFactoryFormulaRevision(ctx context.Context, formulaID string, revision int) (FactoryFormula, FactoryFormulaRevision, error) {
	var formula FactoryFormula
	err := d.db.QueryRowContext(ctx, `SELECT id, name, source, current_revision, archived_at, created_at, updated_at FROM factory_formula WHERE id = ?`, formulaID).
		Scan(&formula.ID, &formula.Name, &formula.Source, &formula.CurrentRevision, &formula.ArchivedAt, &formula.CreatedAt, &formula.UpdatedAt)
	if err != nil {
		return FactoryFormula{}, FactoryFormulaRevision{}, err
	}
	if revision == 0 {
		revision = formula.CurrentRevision
	}
	var saved FactoryFormulaRevision
	err = d.db.QueryRowContext(ctx, `SELECT formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at
		FROM factory_formula_revision WHERE formula_id = ? AND revision = ?`, formulaID, revision).
		Scan(&saved.FormulaID, &saved.Revision, &saved.SchemaVersion, &saved.DefinitionYAML, &saved.ContentHash, &saved.ValidationJSON, &saved.CreatedAt)
	return formula, saved, err
}

func (d *DB) SaveFactoryFormulaRevision(ctx context.Context, id, name, definitionYAML, contentHash, validationJSON string, schemaVersion int, at time.Time) (FactoryFormulaRevision, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return FactoryFormulaRevision{}, err
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
		return FactoryFormulaRevision{}, err
	case source != "custom":
		return FactoryFormulaRevision{}, errors.New("built-in Factory formula is immutable")
	default:
		var existing FactoryFormulaRevision
		err = tx.QueryRowContext(ctx, `SELECT formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at FROM factory_formula_revision WHERE formula_id = ? AND content_hash = ?`, id, contentHash).
			Scan(&existing.FormulaID, &existing.Revision, &existing.SchemaVersion, &existing.DefinitionYAML, &existing.ContentHash, &existing.ValidationJSON, &existing.CreatedAt)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return FactoryFormulaRevision{}, err
		}
		current++
		_, err = tx.ExecContext(ctx, `UPDATE factory_formula SET name = ?, current_revision = ?, archived_at = 0, updated_at = ? WHERE id = ?`, name, current, at.UnixMilli(), id)
	}
	if err != nil {
		return FactoryFormulaRevision{}, err
	}
	if current == 0 {
		current = 1
	}
	saved := FactoryFormulaRevision{FormulaID: id, Revision: current, SchemaVersion: schemaVersion, DefinitionYAML: definitionYAML, ContentHash: contentHash, ValidationJSON: validationJSON, CreatedAt: at.UnixMilli()}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_formula_revision (formula_id, revision, schema_version, definition_yaml, content_hash, validation_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, current, schemaVersion, definitionYAML, contentHash, validationJSON, saved.CreatedAt)
	if err != nil {
		return FactoryFormulaRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return FactoryFormulaRevision{}, err
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
