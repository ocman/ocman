package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) CreateFactoryEpic(ctx context.Context, preferredID, goal, brief, project, instantiationID string, formula model.NativeFormula) (model.NativeEpic, error) {
	return d.createFactoryEpic(ctx, preferredID, goal, brief, project, instantiationID, formula, nil, nil)
}

func (d *DB) CreateFactoryEpicWithProjects(ctx context.Context, preferredID, goal, brief, project, instantiationID string, formula model.NativeFormula, secondary []string) (model.NativeEpic, error) {
	return d.createFactoryEpic(ctx, preferredID, goal, brief, project, instantiationID, formula, secondary, nil)
}

func (d *DB) CreateFactoryEpicWithProjectsAndPermissionRules(ctx context.Context, preferredID, goal, brief, project, instantiationID string, formula model.NativeFormula, secondary []string, rules []model.PermissionRule) (model.NativeEpic, error) {
	return d.createFactoryEpic(ctx, preferredID, goal, brief, project, instantiationID, formula, secondary, rules)
}

func (d *DB) createFactoryEpic(ctx context.Context, preferredID, goal, brief, project, instantiationID string, formula model.NativeFormula, secondary []string, rules []model.PermissionRule) (model.NativeEpic, error) {
	if err := validateNativeFormula(formula, map[string]bool{}); err != nil {
		return model.NativeEpic{}, err
	}
	now := time.Now().UnixMilli()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeEpic{}, fmt.Errorf("beginning Factory Epic creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if instantiationID != "" {
		existing, err := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE instantiation_id = ?`, instantiationID))
		if err == nil {
			existing.Projects, err = listFactoryEpicProjectsWith(ctx, tx, existing.ID)
			if err != nil {
				return model.NativeEpic{}, err
			}
			existing.PermissionRules, err = getFactoryEpicPermissionRules(ctx, tx, existing.ID)
			if err != nil {
				return model.NativeEpic{}, err
			}
			if existing.Goal == goal && existing.Brief == brief && existing.InitialProject == project && factoryProjectSetMatches(existing.Projects, project, secondary) && (rules == nil || slices.Equal(existing.PermissionRules, rules)) {
				return existing, nil
			}
			return model.NativeEpic{}, model.ErrNativeInstantiationConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.NativeEpic{}, fmt.Errorf("looking up Factory instantiation: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_project (path, created_at) VALUES (?, ?) ON CONFLICT(path) DO NOTHING`, project, now); err != nil {
		return model.NativeEpic{}, fmt.Errorf("creating Factory project: %w", err)
	}
	for _, path := range secondary {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_project (path, created_at) VALUES (?, ?) ON CONFLICT(path) DO NOTHING`, path, now); err != nil {
			return model.NativeEpic{}, fmt.Errorf("creating secondary Factory project: %w", err)
		}
	}
	id, err := insertFactoryEpic(ctx, tx, preferredID, goal, brief, project, instantiationID, now)
	if err != nil {
		if errors.Is(err, model.ErrNativeEpicIDTaken) {
			return model.NativeEpic{}, err
		}
		if instantiationID != "" {
			existing, lookupErr := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE instantiation_id = ?`, instantiationID))
			if lookupErr == nil {
				existing.Projects, lookupErr = listFactoryEpicProjectsWith(ctx, tx, existing.ID)
				if lookupErr == nil {
					existing.PermissionRules, lookupErr = getFactoryEpicPermissionRules(ctx, tx, existing.ID)
				}
				if lookupErr == nil && existing.Goal == goal && existing.Brief == brief && existing.InitialProject == project && factoryProjectSetMatches(existing.Projects, project, secondary) && (rules == nil || slices.Equal(existing.PermissionRules, rules)) {
					return existing, nil
				}
			}
			return model.NativeEpic{}, model.ErrNativeInstantiationConflict
		}
		return model.NativeEpic{}, err
	}
	epic := model.NativeEpic{ID: id, Status: "open", Goal: goal, Brief: brief, InitialProject: project, InstantiationID: instantiationID, FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_epic_project (epic_id, project_path, is_epic) VALUES (?, ?, 1)`, id, project); err != nil {
		return model.NativeEpic{}, fmt.Errorf("recording Factory Epic project: %w", err)
	}
	epic.Projects = append(epic.Projects, model.EpicProject{Path: project, Removable: false})
	for _, path := range secondary {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_epic_project (epic_id, project_path, is_epic) VALUES (?, ?, 0)`, id, path); err != nil {
			return model.NativeEpic{}, fmt.Errorf("recording secondary Factory project: %w", err)
		}
		epic.Projects = append(epic.Projects, model.EpicProject{Path: path, Removable: true})
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_epic SET formula_id = ?, formula_version = ?, formula_hash = ? WHERE id = ?`, formula.ID, formula.Version, formula.Hash, id); err != nil {
		return model.NativeEpic{}, fmt.Errorf("recording Factory Formula: %w", err)
	}
	if rules != nil {
		if err := setFactoryEpicPermissionRules(ctx, tx, id, rules); err != nil {
			return model.NativeEpic{}, err
		}
		epic.PermissionRules = append([]model.PermissionRule(nil), rules...)
	}
	if _, err := pourFactoryEpicTx(ctx, tx, epic, formula, now); err != nil {
		return model.NativeEpic{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.NativeEpic{}, fmt.Errorf("committing Factory Epic: %w", err)
	}
	epic.FormulaID, epic.FormulaVersion, epic.FormulaHash = formula.ID, formula.Version, formula.Hash
	return epic, nil
}

func setFactoryEpicPermissionRules(ctx context.Context, tx *sql.Tx, epicID string, rules []model.PermissionRule) error {
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("encoding Factory Epic permission rules: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_epic SET permission_rules_json = ? WHERE id = ?`, string(rulesJSON), epicID); err != nil {
		return fmt.Errorf("storing Factory Epic permission rules: %w", err)
	}
	return nil
}

func getFactoryEpicPermissionRules(ctx context.Context, tx *sql.Tx, epicID string) ([]model.PermissionRule, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT permission_rules_json FROM factory_epic WHERE id = ?`, epicID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("reading Factory Epic permission rules: %w", err)
	}
	var rules []model.PermissionRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("decoding Factory Epic permission rules: %w", err)
	}
	return rules, nil
}

func (d *DB) ListFactoryEpics(ctx context.Context) ([]model.NativeEpic, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("listing Factory Epics: %w", err)
	}
	defer rows.Close()
	var epics []model.NativeEpic
	for rows.Next() {
		epic, err := scanFactoryEpic(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning Factory Epic: %w", err)
		}
		epics = append(epics, epic)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range epics {
		epics[i].Projects, err = d.listFactoryEpicProjects(ctx, epics[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return epics, nil
}

func (d *DB) GetFactoryEpic(ctx context.Context, id string) (model.NativeEpic, error) {
	epic, err := scanFactoryEpic(d.db.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.ErrNativeEpicNotFound
	}
	if err != nil {
		return model.NativeEpic{}, fmt.Errorf("getting Factory Epic: %w", err)
	}
	epic.Projects, err = d.listFactoryEpicProjects(ctx, id)
	if err != nil {
		return model.NativeEpic{}, err
	}
	return epic, nil
}

func (d *DB) listFactoryEpicProjects(ctx context.Context, epicID string) ([]model.EpicProject, error) {
	return listFactoryEpicProjectsWith(ctx, d.db, epicID)
}

type factoryProjectQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listFactoryEpicProjectsWith(ctx context.Context, queryer factoryProjectQueryer, epicID string) ([]model.EpicProject, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT p.project_path, p.is_epic = 0 AND NOT EXISTS (
		SELECT 1 FROM factory_attempt a WHERE a.epic_id = p.epic_id AND a.started_at > 0 AND json_extract(a.frozen_policy_json, '$.repository') = p.project_path
	) AND NOT EXISTS (
		SELECT 1 FROM factory_delivery d WHERE d.epic_id = p.epic_id AND d.project_id = p.project_path
	) AND NOT EXISTS (
		SELECT 1 FROM factory_issue i WHERE i.epic_id = p.epic_id AND i.project_path = p.project_path AND i.status <> 'closed' AND i.kind IN ('implementation', 'task', 'delivery') AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)
	) FROM factory_epic_project p WHERE p.epic_id = ? ORDER BY p.is_epic DESC, p.project_path`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing Factory Epic projects: %w", err)
	}
	defer rows.Close()
	projects := []model.EpicProject{}
	for rows.Next() {
		var project model.EpicProject
		if err := rows.Scan(&project.Path, &project.Removable); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func factoryProjectSetMatches(projects []model.EpicProject, epicProject string, secondary []string) bool {
	if len(projects) != len(secondary)+1 {
		return false
	}
	want := map[string]bool{epicProject: true}
	for _, path := range secondary {
		want[path] = true
	}
	for _, project := range projects {
		if !want[project.Path] {
			return false
		}
	}
	return true
}

func (d *DB) RemoveFactoryEpicProject(ctx context.Context, epicID, project string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var permanent bool
	if err := tx.QueryRowContext(ctx, `SELECT is_epic FROM factory_epic_project WHERE epic_id = ? AND project_path = ?`, epicID, project).Scan(&permanent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrEpicProjectNotFound
		}
		return err
	}
	if permanent {
		return model.ErrEpicProjectPermanent
	}
	var blocked bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM factory_attempt WHERE epic_id = ? AND started_at > 0 AND json_extract(frozen_policy_json, '$.repository') = ?
		UNION ALL SELECT 1 FROM factory_delivery WHERE epic_id = ? AND project_id = ?
		UNION ALL SELECT 1 FROM factory_issue WHERE epic_id = ? AND project_path = ? AND status <> 'closed' AND kind IN ('implementation', 'task', 'delivery') AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)
	)`, epicID, project, epicID, project, epicID, project).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return model.ErrEpicProjectHistory
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM factory_epic_project WHERE epic_id = ? AND project_path = ?`, epicID, project)
	if err != nil {
		if strings.Contains(err.Error(), model.ErrEpicProjectPermanent.Error()) {
			return model.ErrEpicProjectPermanent
		}
		return err
	}
	return tx.Commit()
}
