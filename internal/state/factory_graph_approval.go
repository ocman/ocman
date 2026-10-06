package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// reopenFactoryGraphApprovalTx freezes the edited graph and invalidates its old
// approval in the mutation transaction. Reuse the plan gate and its human UI.
func reopenFactoryGraphApprovalTx(ctx context.Context, tx *sql.Tx, epicID string, baseIssues []model.NativeIssue) error {
	var gateID, project, rationale string
	if err := tx.QueryRowContext(ctx, `SELECT id, project_path FROM factory_issue WHERE epic_id = ? AND kind = 'gate' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id) ORDER BY id LIMIT 1`, epicID).Scan(&gateID, &project); err != nil {
		return err
	}
	err := tx.QueryRowContext(ctx, `SELECT rationale_markdown FROM factory_proposal_revision WHERE epic_id = ? ORDER BY revision DESC LIMIT 1`, epicID).Scan(&rationale)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var baseline int
	err = tx.QueryRowContext(ctx, `SELECT CASE WHEN g.resolution = 'approved' THEN g.proposal_revision ELSE COALESCE(json_extract(p.manifest_json, '$.baseRevision'), 0) END FROM factory_plan_gate g LEFT JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ?`, epicID).Scan(&baseline)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return err
	}
	external, err := factoryGraphExternalReferences(ctx, tx, epicID)
	if err != nil {
		return err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	type node struct {
		Key         string `json:"key"`
		Type        string `json:"type"`
		Requirement string `json:"requirement"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Project     string `json:"project"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Type string `json:"type"`
	}
	manifest := struct {
		EpicID       string `json:"epicId"`
		MolID        string `json:"molId"`
		Project      string `json:"project"`
		Nodes        []node `json:"nodes"`
		Edges        []edge `json:"edges"`
		BaseRevision int    `json:"baseRevision"`
		// Preserve hierarchy and completed history alongside the reviewable work.
		Issues         []model.NativeIssue `json:"issues"`
		ExternalIssues []model.NativeIssue `json:"externalIssues,omitempty"`
		BaseIssues     []model.NativeIssue `json:"baseIssues,omitempty"`
	}{EpicID: epicID, Project: project, Nodes: []node{}, Edges: []edge{}, Issues: issues, ExternalIssues: external, BaseRevision: baseline, BaseIssues: baseIssues}
	work := map[string]bool{}
	for _, issue := range issues {
		if issue.Kind == "mol" && issue.ParentID == "" {
			manifest.MolID = issue.ID
		}
		if issue.Kind != "task" && issue.Kind != "implementation" && issue.Kind != "delivery" {
			continue
		}
		kind := issue.Kind
		if kind == "task" {
			kind = "implementation"
		}
		manifest.Nodes = append(manifest.Nodes, node{issue.ID, kind, issue.Requirement, issue.Title, issue.Description, issue.Project})
		work[issue.ID] = true
	}
	for _, issue := range issues {
		if !work[issue.ID] {
			continue
		}
		for _, dependency := range issue.DependsOn {
			manifest.Edges = append(manifest.Edges, edge{issue.ID, dependency.ID, dependency.Type})
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	content, err := json.Marshal(struct {
		Manifest  json.RawMessage `json:"manifest"`
		Rationale string          `json:"rationaleMarkdown"`
	}{encoded, rationale})
	if err != nil {
		return err
	}
	hash := sha256.Sum256(content)
	hashText := hex.EncodeToString(hash[:])
	var revision int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM factory_proposal_revision WHERE epic_id = ?`, epicID).Scan(&revision); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_proposal_revision (epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, epicID, manifest.MolID, project, revision, string(encoded), rationale, hashText, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(epic_id) DO UPDATE SET proposal_revision = excluded.proposal_revision, proposal_hash = excluded.proposal_hash, outcome = '', resolution = 'open', feedback = '', review_issue_ids_json = '[]', updated_at = excluded.updated_at`, epicID, gateID, revision, hashText, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '', outcome_reason = '' WHERE id = ?`, gateID)
	return err
}

// Initial proposals have no materialized hierarchy. Freeze it before the first
// edit, then retain that baseline through every unapproved amendment.
func factoryAmendmentBaselineTx(ctx context.Context, tx *sql.Tx, epicID string) ([]model.NativeIssue, error) {
	var resolution, encoded string
	err := tx.QueryRowContext(ctx, `SELECT g.resolution, p.manifest_json FROM factory_plan_gate g JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ?`, epicID).Scan(&resolution, &encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var manifest struct {
		Issues     []model.NativeIssue `json:"issues"`
		BaseIssues []model.NativeIssue `json:"baseIssues"`
	}
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &manifest); err != nil {
			return nil, err
		}
		if resolution != "approved" {
			return manifest.BaseIssues, nil
		}
		if manifest.Issues != nil {
			return nil, nil
		}
	}
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return nil, err
	}
	external, err := factoryGraphExternalReferences(ctx, tx, epicID)
	return append(issues, external...), err
}

// External endpoints are frozen references, not work admitted to this Epic.
func factoryGraphExternalReferences(ctx context.Context, tx *sql.Tx, epicID string) ([]model.NativeIssue, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT b.id, b.epic_id, b.project_path, b.kind, b.title, b.description, b.status, b.outcome
		FROM factory_issue_dependency d JOIN factory_issue i ON i.id = d.issue_id JOIN factory_issue b ON b.id = d.depends_on_issue_id
		WHERE i.epic_id = ? AND b.epic_id <> i.epic_id
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id) AND `+effectiveFactoryBlockerSQL+` ORDER BY b.id`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []model.NativeIssue
	for rows.Next() {
		issue := model.NativeIssue{Requirement: "reference", DispatchState: "reference"}
		if err := rows.Scan(&issue.ID, &issue.EpicID, &issue.Project, &issue.Kind, &issue.Title, &issue.Description, &issue.Status, &issue.Outcome); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func blockFactoryGraphApproval(ctx context.Context, reader factoryIssueReader, epicID string, issues []model.NativeIssue) ([]model.NativeIssue, error) {
	rows, err := reader.QueryContext(ctx, `SELECT g.issue_id FROM factory_plan_gate g JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ? AND g.resolution <> 'approved' AND json_type(p.manifest_json, '$.issues') = 'array'`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		var gateID string
		if err := rows.Scan(&gateID); err != nil {
			return nil, err
		}
		for i := range issues {
			issue := &issues[i]
			if issue.Status == "open" && (issue.Kind == "task" || issue.Kind == "implementation" || issue.Kind == "delivery") && issue.DispatchState == "ready" {
				issue.DispatchState = "waiting"
				issue.Blockers = append(issue.Blockers, model.NativeIssueBlocker{ID: gateID, EpicID: epicID, Type: "blocks", Reason: "Waiting for graph approval."})
			}
		}
	}
	return issues, rows.Err()
}
