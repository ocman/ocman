package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

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
	issues := []model.NativeIssue{{ID: molID, EpicID: epic.ID, Project: epic.InitialProject, ParentID: parentID, Requirement: requirement, FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash, Bindings: bindings, Kind: "mol", Title: title, Status: "open"}}
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
		if node.Workflow != nil {
			nodeTitle = node.Workflow.Name
			if nodeTitle == "" {
				nodeTitle = node.Key
			}
			if node.Kind != "plan" {
				description = node.Workflow.Prompt
			}
		}
		issues = append(issues, model.NativeIssue{ID: id, EpicID: epic.ID, Project: epic.InitialProject, ParentID: molID, Requirement: "required", Kind: node.Kind, Title: nodeTitle, Status: "open", Description: description, Workflow: node.Workflow})
	}
	for _, issue := range issues {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, issue.ID, issue.EpicID, issue.Project, issue.Kind, issue.Title, issue.Description, issue.Status, now); err != nil {
			return nil, fmt.Errorf("creating Factory issue: %w", err)
		}
		if issue.Workflow != nil {
			if err := putWorkflowStep(ctx, tx, issue.ID, *issue.Workflow); err != nil {
				return nil, err
			}
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
