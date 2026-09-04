package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (d *DB) AppendFactoryIssueComment(ctx context.Context, epicID, issueID, actor, body string, at time.Time) (model.NativeIssueComment, error) {
	body = strings.TrimSpace(body)
	if epicID == "" || issueID == "" || actor == "" || body == "" || utf8.RuneCountInString(body) > 16000 {
		return model.NativeIssueComment{}, model.ErrInvalidGraphMutation
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_issue WHERE epic_id = ? AND id = ?`, epicID, issueID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NativeIssueComment{}, model.ErrInvalidGraphMutation
		}
		return model.NativeIssueComment{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_comment (issue_id, actor, body, created_at) VALUES (?, ?, ?, ?)`, issueID, actor, body, at.UnixMilli())
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.NativeIssueComment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.NativeIssueComment{}, err
	}
	return model.NativeIssueComment{ID: id, IssueID: issueID, Actor: actor, Body: body, CreatedAt: at.UnixMilli()}, nil
}

func (d *DB) ListFactoryIssueComments(ctx context.Context, epicID, issueID string) ([]model.NativeIssueComment, error) {
	var exists int
	if err := d.db.QueryRowContext(ctx, `SELECT 1 FROM factory_issue WHERE epic_id = ? AND id = ?`, epicID, issueID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrInvalidGraphMutation
		}
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id, issue_id, actor, body, created_at FROM factory_issue_comment WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []model.NativeIssueComment{}
	for rows.Next() {
		var comment model.NativeIssueComment
		if err := rows.Scan(&comment.ID, &comment.IssueID, &comment.Actor, &comment.Body, &comment.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (d *DB) ListNativeFactoryFormulaRevisions(ctx context.Context) ([]model.NativeFormulaRevision, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT r.formula_id, f.name, r.revision, r.source_toml, r.compiled_json, r.content_hash, r.created_at
		FROM factory_native_formula_revision r JOIN factory_native_formula f ON f.id = r.formula_id
		ORDER BY f.name, f.id, r.revision`)
	if err != nil {
		return nil, fmt.Errorf("listing native Factory Formulas: %w", err)
	}
	defer rows.Close()
	var revisions []model.NativeFormulaRevision
	for rows.Next() {
		var revision model.NativeFormulaRevision
		if err := rows.Scan(&revision.FormulaID, &revision.Name, &revision.Revision, &revision.SourceTOML, &revision.CompiledJSON, &revision.ContentHash, &revision.CreatedAt); err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (d *DB) GetNativeFactoryFormulaRevision(ctx context.Context, id string, revision int) (model.NativeFormulaRevision, error) {
	if revision == 0 {
		if err := d.db.QueryRowContext(ctx, `SELECT current_revision FROM factory_native_formula WHERE id = ?`, id).Scan(&revision); err != nil {
			return model.NativeFormulaRevision{}, err
		}
	}
	var saved model.NativeFormulaRevision
	err := d.db.QueryRowContext(ctx, `SELECT r.formula_id, f.name, r.revision, r.source_toml, r.compiled_json, r.content_hash, r.created_at
		FROM factory_native_formula_revision r JOIN factory_native_formula f ON f.id = r.formula_id WHERE r.formula_id = ? AND r.revision = ?`, id, revision).
		Scan(&saved.FormulaID, &saved.Name, &saved.Revision, &saved.SourceTOML, &saved.CompiledJSON, &saved.ContentHash, &saved.CreatedAt)
	return saved, err
}

func (d *DB) SaveNativeFactoryFormulaRevision(ctx context.Context, saved model.NativeFormulaRevision, at time.Time) (model.NativeFormulaRevision, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	err = tx.QueryRowContext(ctx, `SELECT current_revision FROM factory_native_formula WHERE id = ?`, saved.FormulaID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_native_formula (id, name, current_revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, saved.FormulaID, saved.Name, current, at.UnixMilli(), at.UnixMilli())
	} else if err == nil {
		var existing model.NativeFormulaRevision
		err = tx.QueryRowContext(ctx, `SELECT formula_id, ?, revision, source_toml, compiled_json, content_hash, created_at FROM factory_native_formula_revision WHERE formula_id = ? AND content_hash = ?`, saved.Name, saved.FormulaID, saved.ContentHash).
			Scan(&existing.FormulaID, &existing.Name, &existing.Revision, &existing.SourceTOML, &existing.CompiledJSON, &existing.ContentHash, &existing.CreatedAt)
		if err == nil {
			if err = tx.Commit(); err != nil {
				return model.NativeFormulaRevision{}, err
			}
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.NativeFormulaRevision{}, err
		}
		current++
		_, err = tx.ExecContext(ctx, `UPDATE factory_native_formula SET name = ?, current_revision = ?, updated_at = ? WHERE id = ?`, saved.Name, current, at.UnixMilli(), saved.FormulaID)
	}
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	saved.Revision, saved.CreatedAt = current, at.UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_native_formula_revision (formula_id, revision, source_toml, compiled_json, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`, saved.FormulaID, saved.Revision, saved.SourceTOML, saved.CompiledJSON, saved.ContentHash, saved.CreatedAt)
	if err != nil {
		return model.NativeFormulaRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.NativeFormulaRevision{}, err
	}
	return saved, nil
}

func (d *DB) CreateFactoryEpic(ctx context.Context, goal, brief, project, instantiationID string, formula model.NativeFormula) (model.NativeEpic, error) {
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
			if existing.Goal == goal && existing.Brief == brief && existing.InitialProject == project {
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
	id, err := insertFactoryEpic(ctx, tx, goal, brief, project, instantiationID, now)
	if err != nil {
		if instantiationID != "" {
			existing, lookupErr := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE instantiation_id = ?`, instantiationID))
			if lookupErr == nil && existing.Goal == goal && existing.Brief == brief && existing.InitialProject == project {
				return existing, nil
			}
			return model.NativeEpic{}, model.ErrNativeInstantiationConflict
		}
		return model.NativeEpic{}, err
	}
	epic := model.NativeEpic{ID: id, Status: "open", Goal: goal, Brief: brief, InitialProject: project, InstantiationID: instantiationID, FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_epic SET formula_id = ?, formula_version = ?, formula_hash = ? WHERE id = ?`, formula.ID, formula.Version, formula.Hash, id); err != nil {
		return model.NativeEpic{}, fmt.Errorf("recording Factory Formula: %w", err)
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
	return epics, rows.Err()
}

func (d *DB) GetFactoryEpic(ctx context.Context, id string) (model.NativeEpic, error) {
	epic, err := scanFactoryEpic(d.db.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, model.ErrNativeEpicNotFound
	}
	if err != nil {
		return model.NativeEpic{}, fmt.Errorf("getting Factory Epic: %w", err)
	}
	return epic, nil
}

func (d *DB) PourFactoryEpic(ctx context.Context, id string, formula model.NativeFormula) (model.NativeEpic, []model.NativeIssue, error) {
	if err := validateNativeFormula(formula, map[string]bool{}); err != nil {
		return model.NativeEpic{}, nil, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeEpic{}, nil, fmt.Errorf("beginning Factory pour: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	epic, err := scanFactoryEpic(tx.QueryRowContext(ctx, `SELECT id, status, goal, brief, project_path, instantiation_id, formula_id, formula_version, formula_hash FROM factory_epic WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeEpic{}, nil, model.ErrNativeEpicNotFound
	}
	if err != nil {
		return model.NativeEpic{}, nil, fmt.Errorf("getting Factory Epic for pour: %w", err)
	}
	if !matchesFactoryFormulaPin(epic, formula) {
		return model.NativeEpic{}, nil, errors.New("factory formula pin changed before pour")
	}
	var poured int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_issue WHERE epic_id = ?`, id).Scan(&poured); err != nil {
		return model.NativeEpic{}, nil, fmt.Errorf("checking Factory pour: %w", err)
	}
	if poured != 0 {
		if err := tx.Commit(); err != nil {
			return model.NativeEpic{}, nil, fmt.Errorf("committing Factory pour: %w", err)
		}
		issues, err := d.ListFactoryIssues(ctx, id)
		return epic, issues, err
	}
	if _, err := pourFactoryEpicTx(ctx, tx, epic, formula, time.Now().UnixMilli()); err != nil {
		return model.NativeEpic{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_epic SET updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), id); err != nil {
		return model.NativeEpic{}, nil, fmt.Errorf("recording Factory pour: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.NativeEpic{}, nil, fmt.Errorf("committing Factory pour: %w", err)
	}
	issues, err := d.ListFactoryIssues(ctx, id)
	return epic, issues, err
}

func matchesFactoryFormulaPin(epic model.NativeEpic, formula model.NativeFormula) bool {
	if epic.FormulaID != formula.ID || epic.FormulaVersion != formula.Version {
		return false
	}
	if epic.FormulaHash == formula.Hash {
		return true
	}
	// Releases before compiled hashes pinned the built-in Formula's source hash.
	// That pin remains immutable, but is equivalent to the current tracer.
	if formula.ID != "ocman/tracer" || formula.Version != 1 {
		return false
	}
	sum := sha256.Sum256([]byte(formula.Source))
	return epic.FormulaHash == hex.EncodeToString(sum[:])
}

func pourFactoryEpicTx(ctx context.Context, tx *sql.Tx, epic model.NativeEpic, formula model.NativeFormula, now int64) ([]model.NativeIssue, error) {
	inputs := map[string]string{"goal": epic.Goal, "initial_project": epic.InitialProject}
	molID, err := factoryChildID(ctx, tx, epic.ID)
	if err != nil {
		return nil, err
	}
	return pourNativeFormulaTx(ctx, tx, epic, formula, molID, "", "required", epic.Goal, epic.Brief, inputs, inputs, now)
}

func pourNativeFormulaTx(ctx context.Context, tx *sql.Tx, epic model.NativeEpic, formula model.NativeFormula, molID, parentID, requirement, title, brief string, inputs, bindings map[string]string, now int64) ([]model.NativeIssue, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_formula_identity (formula_id, version, source_toml, content_hash, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(formula_id, version) DO NOTHING`, formula.ID, formula.Version, formula.Source, formula.Hash, now); err != nil {
		return nil, fmt.Errorf("recording factory formula identity: %w", err)
	}
	issues := []model.NativeIssue{{ID: molID, EpicID: epic.ID, ParentID: parentID, Requirement: requirement, FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash, Bindings: bindings, Kind: "mol", Title: title, Status: "open"}}
	nodeIDs := make(map[string]string, len(formula.Nodes))
	for _, node := range formula.Nodes {
		id, err := factoryChildID(ctx, tx, molID)
		if err != nil {
			return nil, err
		}
		nodeIDs[node.Key] = id
		nodeTitle, description := node.Kind+": "+title, ""
		if node.Kind == "plan" {
			nodeTitle, description = "Plan: "+title, brief
		}
		if node.Kind == "gate" {
			nodeTitle = "Approval gate"
		}
		issues = append(issues, model.NativeIssue{ID: id, EpicID: epic.ID, ParentID: molID, Requirement: "required", Kind: node.Kind, Title: nodeTitle, Status: "open", Description: description})
	}
	for _, issue := range issues {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, issue.ID, issue.EpicID, issue.Kind, issue.Title, issue.Description, issue.Status, now); err != nil {
			return nil, fmt.Errorf("creating Factory issue: %w", err)
		}
		if issue.Kind == "mol" {
			bindingsJSON, err := json.Marshal(issue.Bindings)
			if err != nil {
				return nil, fmt.Errorf("encoding Factory Mol bindings: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_mol_formula (mol_id, formula_id, formula_version, formula_hash, bindings_json) VALUES (?, ?, ?, ?, ?) ON CONFLICT(mol_id) DO NOTHING`, issue.ID, issue.FormulaID, issue.FormulaVersion, issue.FormulaHash, string(bindingsJSON)); err != nil {
				return nil, fmt.Errorf("recording Factory Mol Formula pin: %w", err)
			}
		}
	}
	for _, issue := range issues {
		if issue.ParentID == "" {
			continue
		}
		index, err := factoryChildIndex(issue.ID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, issue.ParentID, issue.ID, index, issue.Requirement); err != nil {
			return nil, fmt.Errorf("creating Factory hierarchy: %w", err)
		}
	}
	for _, composition := range formula.Composition {
		requirement := composition.Requirement
		if requirement == "" {
			requirement = "required"
		}
		childID, err := factoryChildID(ctx, tx, molID)
		if err != nil {
			return nil, err
		}
		childBindings, err := resolveMolBindings(composition.Formula, composition.Bindings, inputs)
		if err != nil {
			return nil, err
		}
		childTitle := childBindings["goal"]
		if childTitle == "" {
			childTitle = composition.Formula.ID
		}
		child, err := pourNativeFormulaTx(ctx, tx, epic, composition.Formula, childID, molID, requirement, childTitle, "", childBindings, childBindings, now)
		if err != nil {
			return nil, err
		}
		issues = append(issues, child...)
	}
	for _, edge := range formula.Edges {
		edgeType := edge.Type
		if edgeType == "" {
			edgeType = "blocks"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, nodeIDs[edge.From], nodeIDs[edge.To], edgeType); err != nil {
			return nil, fmt.Errorf("creating Factory dependency: %w", err)
		}
	}
	return issues, nil
}

func resolveMolBindings(formula model.NativeFormula, bindings, inputs map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(formula.Inputs))
	for _, input := range formula.Inputs {
		expression, ok := bindings[input]
		if !ok {
			return nil, fmt.Errorf("nested Mol is missing binding for %s", input)
		}
		value, ok := inputs[expression]
		if !ok {
			return nil, fmt.Errorf("nested Mol binding %s is unresolved", input)
		}
		resolved[input] = value
	}
	for input := range bindings {
		if !containsFormulaInput(formula.Inputs, input) {
			return nil, fmt.Errorf("nested Mol binding %s is unresolved", input)
		}
	}
	return resolved, nil
}

func containsFormulaInput(inputs []string, want string) bool {
	for _, input := range inputs {
		if input == want {
			return true
		}
	}
	return false
}

func validateNativeFormula(formula model.NativeFormula, ancestors map[string]bool) error {
	if formula.ID == "" || formula.Version < 1 || formula.Hash == "" {
		return errors.New("factory formula identity is incomplete")
	}
	identity := fmt.Sprintf("%s@%d", formula.ID, formula.Version)
	if ancestors[identity] {
		return errors.New("factory formula composition cycle")
	}
	ancestors[identity] = true
	defer delete(ancestors, identity)
	nodes := make(map[string]bool, len(formula.Nodes))
	for _, node := range formula.Nodes {
		if !model.ValidNativeFormulaKey(node.Key) || node.Kind == "" || nodes[node.Key] {
			return errors.New("factory formula contains invalid issue keys")
		}
		nodes[node.Key] = true
	}
	for _, edge := range formula.Edges {
		if !nodes[edge.From] || !nodes[edge.To] {
			return errors.New("factory formula edge references a missing issue")
		}
		if edge.Type != "" && edge.Type != "blocks" && edge.Type != "on_failure" {
			return errors.New("factory formula edge type is invalid")
		}
	}
	for _, composition := range formula.Composition {
		if !model.ValidNativeFormulaKey(composition.Key) || nodes[composition.Key] {
			return errors.New("factory formula composition key must be unique")
		}
		if composition.Requirement != "" && composition.Requirement != "required" && composition.Requirement != "optional" && composition.Requirement != "reference" {
			return errors.New("factory formula composition requirement must be required, optional, or reference")
		}
		nodes[composition.Key] = true
		for _, input := range composition.Formula.Inputs {
			binding, ok := composition.Bindings[input]
			if !ok || !containsFormulaInput(formula.Inputs, binding) {
				return fmt.Errorf("nested Mol binding %s is unresolved", input)
			}
		}
		if err := validateNativeFormula(composition.Formula, ancestors); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) ListFactoryIssues(ctx context.Context, epicID string) ([]model.NativeIssue, error) {
	if _, err := d.GetFactoryEpic(ctx, epicID); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT i.id, i.epic_id, COALESCE(h.parent_issue_id, ''), COALESCE(h.requirement, ''), i.kind, i.title, i.status, i.description, COALESCE(f.formula_id, ''), COALESCE(f.formula_version, 0), COALESCE(f.formula_hash, ''), COALESCE(f.bindings_json, '{}'), COALESCE(m.proposal_revision, 0), COALESCE(m.manifest_key, ''), i.outcome, i.outcome_reason, COALESCE(g.resolution, ''), i.retry_at, i.retry_attempts, i.created_at FROM factory_issue i LEFT JOIN factory_issue_hierarchy h ON h.child_issue_id = i.id LEFT JOIN factory_mol_formula f ON f.mol_id = i.id LEFT JOIN factory_materialization m ON m.implementation_issue_id = i.id LEFT JOIN factory_plan_gate g ON g.issue_id = i.id LEFT JOIN factory_removed_issue r ON r.issue_id = i.id WHERE i.epic_id = ? AND r.issue_id IS NULL ORDER BY CASE i.kind WHEN 'mol' THEN 0 WHEN 'plan' THEN 1 WHEN 'gate' THEN 2 ELSE 3 END`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing Factory issues: %w", err)
	}
	defer rows.Close()
	var issues []model.NativeIssue
	for rows.Next() {
		var issue model.NativeIssue
		var bindings string
		if err := rows.Scan(&issue.ID, &issue.EpicID, &issue.ParentID, &issue.Requirement, &issue.Kind, &issue.Title, &issue.Status, &issue.Description, &issue.FormulaID, &issue.FormulaVersion, &issue.FormulaHash, &bindings, &issue.PlanRevision, &issue.ManifestKey, &issue.Outcome, &issue.OutcomeReason, &issue.GateResolution, &issue.RetryAt, &issue.RetryAttempts, &issue.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning Factory issue: %w", err)
		}
		if err := json.Unmarshal([]byte(bindings), &issue.Bindings); err != nil {
			return nil, fmt.Errorf("decoding Factory Mol bindings: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.deriveFactoryIssueDispatch(ctx, epicID, issues)
}

// ListRemovedFactoryIssues preserves the details of soft-deleted work for audit.
func (d *DB) ListRemovedFactoryIssues(ctx context.Context, epicID string) ([]model.NativeIssue, error) {
	if _, err := d.GetFactoryEpic(ctx, epicID); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT i.id, i.epic_id, i.kind, i.title, i.description, i.status, i.outcome, i.outcome_reason, r.removed_at FROM factory_removed_issue r JOIN factory_issue i ON i.id = r.issue_id WHERE i.epic_id = ? ORDER BY r.removed_at DESC, i.id`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing removed Factory issues: %w", err)
	}
	defer rows.Close()
	var issues []model.NativeIssue
	for rows.Next() {
		var issue model.NativeIssue
		if err := rows.Scan(&issue.ID, &issue.EpicID, &issue.Kind, &issue.Title, &issue.Description, &issue.Status, &issue.Outcome, &issue.OutcomeReason, &issue.RemovedAt); err != nil {
			return nil, fmt.Errorf("scanning removed Factory issue: %w", err)
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func (d *DB) deriveFactoryIssueDispatch(ctx context.Context, epicID string, issues []model.NativeIssue) ([]model.NativeIssue, error) {
	byID := make(map[string]*model.NativeIssue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
		issues[i].DispatchState = "waiting"
	}
	rows, err := d.db.QueryContext(ctx, `SELECT d.issue_id, d.type, b.id, b.epic_id, b.kind, b.status, b.outcome, b.outcome_reason, COALESCE(g.resolution, '') FROM factory_issue_dependency d JOIN factory_issue b ON b.id = d.depends_on_issue_id LEFT JOIN factory_plan_gate g ON g.issue_id = b.id WHERE d.issue_id IN (SELECT id FROM factory_issue WHERE epic_id = ?) AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = b.id)`, epicID)
	if err != nil {
		return nil, err
	}
	notApplicable := map[string]bool{}
	waiting := map[string]bool{}
	blocked := map[string]bool{}
	for rows.Next() {
		var issueID, edgeType, blockerID, blockerEpicID, kind, status, outcome, reason, resolution string
		if err := rows.Scan(&issueID, &edgeType, &blockerID, &blockerEpicID, &kind, &status, &outcome, &reason, &resolution); err != nil {
			rows.Close()
			return nil, err
		}
		issue := byID[issueID]
		if issue == nil || issue.Status != "open" || factoryIssueRequirement(issue, byID) == "reference" {
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
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Reason: reason, Outcome: outcome})
			} else {
				waiting[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Reason: reason, Outcome: outcome})
			}
		default: // on_failure
			if failed {
				continue
			}
			if status == "closed" {
				notApplicable[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Reason: reason, Outcome: outcome})
			} else {
				waiting[issueID] = true
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: blockerID, EpicID: blockerEpicID, Reason: reason, Outcome: outcome})
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range issues {
		issue := &issues[i]
		if factoryIssueRequirement(issue, byID) == "reference" {
			issue.DispatchState = "reference"
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

// MutateFactoryGraph applies graph edits atomically. In-progress and closed
// Issues are immutable; all other lifecycle states remain editable.
func (d *DB) MutateFactoryGraph(ctx context.Context, m model.GraphMutation) error {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if m.EpicID == "" {
		return invalid("factory epic is required for structural mutation")
	}
	var epicStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM factory_epic WHERE id = ?`, m.EpicID).Scan(&epicStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("factory epic is unavailable for structural mutation")
		}
		return fmt.Errorf("reading Factory Epic for structural mutation: %w", err)
	}
	if epicStatus != "open" {
		return invalid("factory epic is unavailable for structural mutation")
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
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) VALUES (?, ?, ?, ?, ?, 'open', ?)`, id, epicID, m.Kind, m.Title, m.Description, time.Now().UnixMilli()); err != nil {
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
		if _, err := openIssue(m.IssueID, true); err != nil {
			return err
		}
		switch m.Action {
		case "edit":
			if strings.TrimSpace(m.Title) == "" {
				return invalid("factory issue title is required")
			}
			_, err = tx.ExecContext(ctx, `UPDATE factory_issue SET title = ?, description = ? WHERE id = ?`, m.Title, m.Description, m.IssueID)
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
			if _, err = openIssue(m.DependsOnID, false); err == nil {
				var cycle bool
				if m.DependencyType != "blocks" && m.DependencyType != "on_failure" {
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
				SELECT 1 FROM lineage JOIN factory_materialization m ON m.implementation_issue_id = lineage.id))`, epicID)
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
		WHERE id = ? AND epic_id = ? AND kind IN ('task', 'implementation') AND status = 'closed' AND outcome IN ('failed', 'cancelled')
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
	result, err := d.db.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome_reason = '' WHERE id = ? AND epic_id = ? AND status = 'deferred'`, issueID, epicID)
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
	_, err := tx.ExecContext(ctx, descendants+` UPDATE factory_issue SET status = 'closed', outcome = 'cancelled', outcome_reason = 'container_closed_without_execution'
		WHERE id IN (SELECT d.id FROM descendants d WHERE d.required = 0) AND status NOT IN ('closed', 'in_progress')`, molID)
	return err
}

// CloseFactoryEpic requires the root Mol to have been explicitly closed.
func (d *DB) CloseFactoryEpic(ctx context.Context, epicID string) error {
	result, err := d.db.ExecContext(ctx, `UPDATE factory_epic SET status = 'closed', updated_at = ?
		WHERE id = ? AND status = 'open' AND EXISTS (
			SELECT 1 FROM factory_issue i WHERE i.epic_id = factory_epic.id AND i.kind = 'mol' AND i.status = 'closed' AND i.outcome = 'succeeded'
				AND NOT EXISTS (SELECT 1 FROM factory_issue_hierarchy h WHERE h.child_issue_id = i.id)
		)`, time.Now().UnixMilli(), epicID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("factory Epic requires successful explicit Mol closure")
	}
	return nil
}

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
	var acknowledged int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_local_execution_ack WHERE host_id = 'local' AND repo_root = ? AND profile_id = 'factory-implement' AND profile_version = 'v1'`, epic.InitialProject).Scan(&acknowledged); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation requires local execution acknowledgement")
	}
	var kind, status string
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(parent_id, requirement) AS (
		SELECT parent_issue_id, requirement FROM factory_issue_hierarchy WHERE child_issue_id = ?
		UNION ALL
		SELECT h.parent_issue_id, h.requirement FROM factory_issue_hierarchy h JOIN ancestors a ON h.child_issue_id = a.parent_id
		) SELECT i.kind, i.status FROM factory_issue i WHERE i.id = ? AND i.epic_id = ?
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)
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
			)
		)`, issueID, issueID, epicID).Scan(&kind, &status); err != nil || (kind != "implementation" && kind != "task") || status != "open" {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory implementation issue is not ready")
	}
	policy := model.FactoryCapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4}
	_ = tx.QueryRowContext(ctx, `SELECT global_capacity, project_capacity FROM factory_capacity_policy WHERE id = 1`).Scan(&policy.GlobalCapacity, &policy.ProjectCapacity)
	var override int
	if err := tx.QueryRowContext(ctx, `SELECT capacity FROM factory_project_capacity_override WHERE project_path = ?`, epic.InitialProject).Scan(&override); err == nil {
		policy.ProjectCapacity = override
	}
	var global, project int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN json_extract(frozen_policy_json, '$.repository') = ? THEN 1 ELSE 0 END), 0) FROM factory_attempt a WHERE phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1' AND NOT EXISTS (SELECT 1 FROM factory_recovery_gate r WHERE r.attempt_id = a.id AND r.resolution = 'open') AND NOT EXISTS (SELECT 1 FROM factory_authority_escalation_gate g WHERE g.attempt_id = a.id AND g.resolution NOT IN ('approve', 'reject'))`, epic.InitialProject).Scan(&global, &project); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	var epicActive int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_attempt WHERE epic_id = ? AND phase IN ('prepared', 'active', 'stopping') AND json_extract(frozen_policy_json, '$.profile') = 'factory-implement/v1'`, epicID).Scan(&epicActive); err != nil {
		return model.NativeEpic{}, model.FactoryAttempt{}, err
	}
	if epicActive != 0 {
		return model.NativeEpic{}, model.FactoryAttempt{}, errors.New("factory Epic workspace is in use")
	}
	if global >= policy.GlobalCapacity || project >= policy.ProjectCapacity {
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
	policyJSON, err := json.Marshal(model.FactoryAttemptPolicy{Repository: epic.InitialProject, Profile: profile})
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
	return epic, model.FactoryAttempt{ID: id, EpicID: epicID, WorkID: issueID, Sequence: sequence, Phase: model.FactoryAttemptPrepared, FrozenPolicy: model.FactoryAttemptPolicy{Repository: epic.InitialProject, Profile: profile}, CreatedAt: now, UpdatedAt: now, AgentToken: agentToken}, nil
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

func (d *DB) SaveFactoryProposalRevision(ctx context.Context, proposal model.NativeProposalRevision) (model.NativeProposalRevision, error) {
	proposal, _, err := d.saveFactoryProposalRevision(ctx, proposal, "", "")
	return proposal, err
}

func (d *DB) SaveFactoryProposalRevisionForAttempt(ctx context.Context, proposal model.NativeProposalRevision, attemptID, token string) (model.NativeProposalRevision, bool, error) {
	return d.saveFactoryProposalRevision(ctx, proposal, attemptID, token)
}

func (d *DB) saveFactoryProposalRevision(ctx context.Context, proposal model.NativeProposalRevision, attemptID, token string) (model.NativeProposalRevision, bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("beginning Factory proposal submission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if attemptID != "" {
		var authorized int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_attempt a JOIN factory_external_mapping m ON m.system = 'factory' AND m.external_kind = 'attempt_token' AND m.external_id = ? AND m.entity_kind = 'attempt' AND m.entity_id = a.id WHERE a.id = ? AND a.epic_id = ? AND a.phase = 'active' AND json_extract(a.frozen_policy_json, '$.profile') = 'factory-plan/v1' AND NOT EXISTS (SELECT 1 FROM factory_plan_gate g WHERE g.epic_id = a.epic_id AND g.resolution IN ('approved', 'rejected'))`, token, attemptID, proposal.EpicID).Scan(&authorized)
		if errors.Is(err, sql.ErrNoRows) {
			return model.NativeProposalRevision{}, false, nil
		}
		if err != nil {
			return model.NativeProposalRevision{}, false, err
		}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM factory_epic WHERE id = ?`, proposal.EpicID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.NativeProposalRevision{}, false, model.ErrNativeEpicNotFound
		}
		return model.NativeProposalRevision{}, false, fmt.Errorf("getting Factory Epic for proposal: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM factory_proposal_revision WHERE epic_id = ?`, proposal.EpicID).Scan(&proposal.Revision); err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("allocating Factory proposal revision: %w", err)
	}
	proposal.CreatedAt = time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_proposal_revision (epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, proposal.EpicID, proposal.MolID, proposal.Project, proposal.Revision, proposal.ManifestJSON, proposal.RationaleMarkdown, proposal.ContentHash, proposal.CreatedAt); err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("saving Factory proposal revision: %w", err)
	}
	var gateID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM factory_issue WHERE epic_id = ? AND kind = 'gate' ORDER BY id LIMIT 1`, proposal.EpicID).Scan(&gateID); err == nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(epic_id) DO UPDATE SET proposal_revision = excluded.proposal_revision, proposal_hash = excluded.proposal_hash, outcome = '', resolution = 'open', feedback = '', review_issue_ids_json = '[]', updated_at = excluded.updated_at`, proposal.EpicID, gateID, proposal.Revision, proposal.ContentHash, proposal.CreatedAt); err != nil {
			return model.NativeProposalRevision{}, false, fmt.Errorf("resetting Factory Plan gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open' WHERE id = ?`, gateID); err != nil {
			return model.NativeProposalRevision{}, false, fmt.Errorf("reopening Factory Plan gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '', outcome_reason = '' WHERE epic_id = ? AND kind = 'materialization' AND status = 'closed'`, proposal.EpicID); err != nil {
			return model.NativeProposalRevision{}, false, fmt.Errorf("reopening Factory materialization: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("committing Factory proposal submission: %w", err)
	}
	return proposal, true, nil
}

func (d *DB) GetFactoryPlanGate(ctx context.Context, epicID string) (model.NativePlanGate, error) {
	return scanFactoryPlanGate(d.db.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json FROM factory_plan_gate WHERE epic_id = ?`, epicID))
}

// MaterializeFactoryPlan creates the approved implementation graph and closes
// its Materialization Issue in the same transaction.
func (d *DB) MaterializeFactoryPlan(ctx context.Context, epicID, issueID, profile string, at time.Time) (model.NativeMaterialization, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeMaterialization{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var gate model.NativePlanGate
	gate, err = scanFactoryPlanGate(tx.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json FROM factory_plan_gate WHERE epic_id = ?`, epicID))
	if err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("reading Factory Plan approval: %w", err)
	}
	if gate.Resolution != "approved" || gate.Outcome != "succeeded" {
		return model.NativeMaterialization{}, errors.New("factory Plan approval is unavailable")
	}
	var existing model.NativeMaterialization
	err = tx.QueryRowContext(ctx, `SELECT id, epic_id, issue_id, proposal_revision, proposal_hash, manifest_key, implementation_issue_id FROM factory_materialization WHERE issue_id = ? AND proposal_revision = ? AND proposal_hash = ?`, issueID, gate.ProposalRevision, gate.ProposalHash).
		Scan(&existing.ID, &existing.EpicID, &existing.IssueID, &existing.ProposalRevision, &existing.ProposalHash, &existing.ManifestKey, &existing.ImplementationID)
	if err == nil {
		if existing.EpicID != epicID || profile != "factory-materialize/v1" {
			return model.NativeMaterialization{}, errors.New("factory materialization conflicts with recorded transaction")
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.NativeMaterialization{}, err
	}
	if profile != "factory-materialize/v1" {
		return model.NativeMaterialization{}, errors.New("factory materialization requires factory-materialize/v1")
	}

	var kind, status string
	if err := tx.QueryRowContext(ctx, `SELECT kind, status FROM factory_issue WHERE id = ? AND epic_id = ?`, issueID, epicID).Scan(&kind, &status); err != nil {
		return model.NativeMaterialization{}, errors.New("factory materialization issue is unavailable")
	}
	if kind != "materialization" || status != "open" {
		return model.NativeMaterialization{}, errors.New("factory materialization issue is not ready")
	}
	var proposal model.NativeProposalRevision
	if err := tx.QueryRowContext(ctx, `SELECT epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at FROM factory_proposal_revision WHERE epic_id = ? AND revision = ? AND content_hash = ?`, epicID, gate.ProposalRevision, gate.ProposalHash).
		Scan(&proposal.EpicID, &proposal.MolID, &proposal.Project, &proposal.Revision, &proposal.ManifestJSON, &proposal.RationaleMarkdown, &proposal.ContentHash, &proposal.CreatedAt); err != nil {
		return model.NativeMaterialization{}, errors.New("factory approved Plan revision is unavailable")
	}
	var manifest struct {
		EpicID  string `json:"epicId"`
		MolID   string `json:"molId"`
		Project string `json:"project"`
		Nodes   []struct {
			Key         string `json:"key"`
			Type        string `json:"type"`
			Requirement string `json:"requirement"`
			Pinned      bool   `json:"pinned"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(proposal.ManifestJSON), &manifest); err != nil || manifest.EpicID != epicID || manifest.MolID != proposal.MolID || manifest.Project != proposal.Project {
		return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
	}
	implementationKey := ""
	for _, node := range manifest.Nodes {
		if node.Pinned && node.Requirement != "reference" {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
		if node.Type == "implementation" && node.Requirement == "required" {
			if implementationKey != "" || !model.ValidNativeFormulaKey(node.Key) {
				return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
			}
			implementationKey = node.Key
		}
	}
	if implementationKey == "" {
		return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
	}
	var goal string
	if err := tx.QueryRowContext(ctx, `SELECT goal FROM factory_epic WHERE id = ?`, epicID).Scan(&goal); err != nil {
		return model.NativeMaterialization{}, err
	}
	id, err := factoryGraphID("fm_")
	if err != nil {
		return model.NativeMaterialization{}, err
	}
	now := at.UnixMilli()
	implementationID, err := factoryChildID(ctx, tx, proposal.MolID)
	if err != nil {
		return model.NativeMaterialization{}, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_materialization m JOIN factory_issue i ON i.id = m.implementation_issue_id WHERE m.epic_id = ? AND m.implementation_issue_id <> ? AND i.status = 'in_progress' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)`, epicID, implementationID).Scan(&active); err != nil {
		return model.NativeMaterialization{}, err
	}
	if active != 0 {
		return model.NativeMaterialization{}, errors.New("factory approved Plan cannot remove active implementation work")
	}
	// Superseded implementations take their descendants with them; an orphan
	// whose parent is removed would break the requirement walk in listings.
	if _, err := tx.ExecContext(ctx, `WITH RECURSIVE superseded(id) AS (
			SELECT m.implementation_issue_id FROM factory_materialization m WHERE m.epic_id = ? AND m.implementation_issue_id <> ?
			UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN superseded s ON h.parent_issue_id = s.id)
		INSERT INTO factory_removed_issue (issue_id, plan_id, plan_revision, removed_at)
		SELECT id, ?, ?, ? FROM superseded WHERE true
		ON CONFLICT(issue_id) DO NOTHING`, epicID, implementationID, epicID, proposal.Revision, now); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("removing superseded Factory implementation: %w", err)
	}
	result := model.NativeMaterialization{ID: id, EpicID: epicID, IssueID: issueID, ProposalRevision: proposal.Revision, ProposalHash: proposal.ContentHash, ManifestKey: implementationKey, ImplementationID: implementationID}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, kind, title, description, status, created_at) VALUES (?, ?, 'implementation', ?, ?, 'open', ?)`, implementationID, epicID, "Implementation: "+goal, proposal.RationaleMarkdown, now); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("creating Factory implementation: %w", err)
	}
	implementationIndex, err := factoryChildIndex(implementationID)
	if err != nil {
		return model.NativeMaterialization{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, proposal.MolID, implementationID, implementationIndex); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("adding Factory implementation closure: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks')`, implementationID, issueID); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("adding Factory implementation dependency: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_materialization (id, epic_id, issue_id, proposal_revision, proposal_hash, manifest_key, profile, implementation_issue_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, epicID, issueID, proposal.Revision, proposal.ContentHash, implementationKey, profile, implementationID, now); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("recording Factory materialization: %w", err)
	}
	for _, entity := range []struct{ kind, id string }{{"issue", implementationID}, {"hierarchy", proposal.MolID + "\x00" + implementationID}, {"dependency", implementationID + "\x00" + issueID}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_materialization_provenance (entity_kind, entity_id, plan_id, plan_revision, materialization_id, manifest_key) VALUES (?, ?, ?, ?, ?, ?)`, entity.kind, entity.id, epicID, proposal.Revision, id, implementationKey); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("recording Factory materialization provenance: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ? AND status = 'open'`, issueID); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("closing Factory materialization: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.NativeMaterialization{}, err
	}
	return result, nil
}

func (d *DB) DecideFactoryPlanGate(ctx context.Context, epicID, action string, revision int, hash, feedback string) (model.NativePlanGate, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativePlanGate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := scanFactoryPlanGate(tx.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json FROM factory_plan_gate WHERE epic_id = ?`, epicID))
	if err != nil {
		return model.NativePlanGate{}, err
	}
	if gate.ProposalRevision != revision || gate.ProposalHash != hash {
		return model.NativePlanGate{}, errors.New("factory Plan revision is stale")
	}
	if (action == "approve" && gate.Resolution == "approved") || (action == "reject" && gate.Resolution == "rejected") {
		return gate, tx.Commit()
	}
	if gate.Resolution == "approved" || gate.Resolution == "rejected" {
		return model.NativePlanGate{}, errors.New("factory Plan gate is already resolved")
	}
	if action == "approve" && gate.Resolution == "revision_requested" {
		return model.NativePlanGate{}, errors.New("factory Plan requires a new proposal revision")
	}
	if action == "approve" {
		gate.Outcome, gate.Resolution = "succeeded", "approved"
	}
	if action == "revise" {
		gate.Outcome, gate.Resolution = "", "revision_requested"
	}
	if action == "reject" {
		gate.Outcome, gate.Resolution = "failed", "rejected"
		rows, err := tx.QueryContext(ctx, `SELECT id FROM factory_issue WHERE epic_id = ? AND status NOT IN ('open', 'cancelled', 'closed') AND id <> ? ORDER BY id`, epicID, gate.IssueID)
		if err != nil {
			return model.NativePlanGate{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return model.NativePlanGate{}, err
			}
			gate.ReviewIssueIDs = append(gate.ReviewIssueIDs, id)
		}
		if err := rows.Close(); err != nil {
			return model.NativePlanGate{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'cancelled', outcome_reason = 'Plan rejected' WHERE epic_id = ? AND status = 'open' AND id <> ?`, epicID, gate.IssueID); err != nil {
			return model.NativePlanGate{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_epic SET status = 'closed', updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), epicID); err != nil {
			return model.NativePlanGate{}, err
		}
	}
	if action != "approve" && action != "revise" && action != "reject" {
		return model.NativePlanGate{}, errors.New("invalid factory Plan gate action")
	}
	gate.Feedback = feedback
	review, err := json.Marshal(gate.ReviewIssueIDs)
	if err != nil {
		return model.NativePlanGate{}, err
	}
	status, outcome := "open", ""
	if action != "revise" {
		status = "closed"
		outcome = gate.Outcome
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_plan_gate SET outcome = ?, resolution = ?, feedback = ?, review_issue_ids_json = ?, updated_at = ? WHERE epic_id = ?`, gate.Outcome, gate.Resolution, gate.Feedback, string(review), time.Now().UnixMilli(), epicID); err != nil {
		return model.NativePlanGate{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = ?, outcome = ? WHERE id = ?`, status, outcome, gate.IssueID); err != nil {
		return model.NativePlanGate{}, err
	}
	if action == "approve" {
		if err := closePlanOnApprovalTx(ctx, tx, epicID, time.Now().UnixMilli()); err != nil {
			return model.NativePlanGate{}, err
		}
		if err := closeHandBuiltMaterializationTx(ctx, tx, epicID); err != nil {
			return model.NativePlanGate{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.NativePlanGate{}, err
	}
	return gate, nil
}

type factoryPlanGateScanner interface{ Scan(...any) error }

func scanFactoryPlanGate(scanner factoryPlanGateScanner) (model.NativePlanGate, error) {
	var gate model.NativePlanGate
	var review string
	if err := scanner.Scan(&gate.EpicID, &gate.IssueID, &gate.ProposalRevision, &gate.ProposalHash, &gate.Outcome, &gate.Resolution, &gate.Feedback, &review); err != nil {
		return model.NativePlanGate{}, err
	}
	if err := json.Unmarshal([]byte(review), &gate.ReviewIssueIDs); err != nil {
		return model.NativePlanGate{}, err
	}
	return gate, nil
}

func (d *DB) GetFactoryProposalRevision(ctx context.Context, epicID string, revision int) (model.NativeProposalRevision, error) {
	var proposal model.NativeProposalRevision
	query := `SELECT epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at FROM factory_proposal_revision WHERE epic_id = ?`
	args := []any{epicID}
	if revision == 0 {
		query += ` ORDER BY revision DESC LIMIT 1`
	} else {
		query += ` AND revision = ?`
		args = append(args, revision)
	}
	err := d.db.QueryRowContext(ctx, query, args...).Scan(&proposal.EpicID, &proposal.MolID, &proposal.Project, &proposal.Revision, &proposal.ManifestJSON, &proposal.RationaleMarkdown, &proposal.ContentHash, &proposal.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NativeProposalRevision{}, errors.New("factory proposal revision not found")
	}
	if err != nil {
		return model.NativeProposalRevision{}, fmt.Errorf("getting Factory proposal revision: %w", err)
	}
	return proposal, nil
}

func (d *DB) ListFactoryProposalRevisions(ctx context.Context, epicID string) ([]model.NativeProposalRevision, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at FROM factory_proposal_revision WHERE epic_id = ? ORDER BY revision`, epicID)
	if err != nil {
		return nil, fmt.Errorf("listing Factory proposal revisions: %w", err)
	}
	defer rows.Close()
	var proposals []model.NativeProposalRevision
	for rows.Next() {
		var proposal model.NativeProposalRevision
		if err := rows.Scan(&proposal.EpicID, &proposal.MolID, &proposal.Project, &proposal.Revision, &proposal.ManifestJSON, &proposal.RationaleMarkdown, &proposal.ContentHash, &proposal.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning Factory proposal revision: %w", err)
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating Factory proposal revisions: %w", err)
	}
	return proposals, nil
}

type factoryEpicScanner interface{ Scan(...any) error }

func scanFactoryEpic(scanner factoryEpicScanner) (model.NativeEpic, error) {
	var epic model.NativeEpic
	if err := scanner.Scan(&epic.ID, &epic.Status, &epic.Goal, &epic.Brief, &epic.InitialProject, &epic.InstantiationID, &epic.FormulaID, &epic.FormulaVersion, &epic.FormulaHash); err != nil {
		return model.NativeEpic{}, err
	}
	return epic, nil
}

func factoryGraphID(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generating Factory ID: %w", err)
	}
	return prefix + hex.EncodeToString(random[:]), nil
}

func insertFactoryEpic(ctx context.Context, tx *sql.Tx, goal, brief, project, instantiationID string, now int64) (string, error) {
	for {
		id, err := factoryEpicID(goal)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_epic (id, project_path, status, goal, brief, instantiation_id, created_at, updated_at) VALUES (?, ?, 'open', ?, ?, ?, ?, ?)`, id, project, goal, brief, instantiationID, now, now)
		if err == nil {
			return id, nil
		}
		if strings.Contains(err.Error(), "UNIQUE constraint failed: factory_epic.id") {
			continue
		}
		return "", fmt.Errorf("creating Factory Epic: %w", err)
	}
}

func factoryEpicID(goal string) (string, error) {
	initials := make([]byte, 0, 10)
	inWord := false
	for i := 0; i < len(goal) && len(initials) < 10; i++ {
		c := goal[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			if !inWord {
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				initials = append(initials, c)
			}
			inWord = true
		} else {
			inWord = false
		}
	}
	if len(initials) == 0 {
		initials = []byte("epic")
	}
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generating Factory Epic ID: %w", err)
	}
	suffix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random[:]))[:4]
	return string(initials) + "-" + suffix, nil
}

func factoryChildID(ctx context.Context, tx *sql.Tx, parentID string) (string, error) {
	var index int
	err := tx.QueryRowContext(ctx, `SELECT next_index FROM factory_issue_child_sequence WHERE parent_issue_id = ?`, parentID).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		index = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_issue_child_sequence (parent_issue_id, next_index) VALUES (?, 2)`, parentID)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE factory_issue_child_sequence SET next_index = ? WHERE parent_issue_id = ?`, index+1, parentID)
	}
	if err != nil {
		return "", fmt.Errorf("allocating Factory child index: %w", err)
	}
	return fmt.Sprintf("%s.%d", parentID, index), nil
}

func factoryChildIndex(id string) (int, error) {
	index, err := strconv.Atoi(id[strings.LastIndex(id, ".")+1:])
	if err != nil || index < 1 {
		return 0, fmt.Errorf("invalid Factory child ID %q", id)
	}
	return index, nil
}
