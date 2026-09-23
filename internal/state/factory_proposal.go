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

func (d *DB) SaveFactoryProposalRevision(ctx context.Context, proposal model.NativeProposalRevision) (model.NativeProposalRevision, error) {
	proposal, _, err := d.saveFactoryProposalRevision(ctx, proposal, "", "", false)
	return proposal, err
}

func (d *DB) SaveFactoryProposalRevisionForAttempt(ctx context.Context, proposal model.NativeProposalRevision, attemptID, token string) (model.NativeProposalRevision, bool, error) {
	return d.saveFactoryProposalRevision(ctx, proposal, attemptID, token, false)
}

// ImportFactoryProposalRevision completes unclaimed planning work and opens its
// approval gate atomically. No planning or implementation attempt is launched.
func (d *DB) ImportFactoryProposalRevision(ctx context.Context, proposal model.NativeProposalRevision) (model.NativeProposalRevision, bool, error) {
	return d.saveFactoryProposalRevision(ctx, proposal, "", "", true)
}

func (d *DB) saveFactoryProposalRevision(ctx context.Context, proposal model.NativeProposalRevision, attemptID, token string, imported bool) (model.NativeProposalRevision, bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("beginning Factory proposal submission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if imported {
		// Completing the Plan in this transaction prevents a concurrent human
		// claim from starting a planner for a proposal already awaiting review.
		result, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = 'Existing plan imported'
			WHERE epic_id = ? AND kind = 'plan' AND (status = 'open' OR (status = 'closed' AND outcome = 'succeeded'))
			AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id)
			AND EXISTS (SELECT 1 FROM factory_epic WHERE id = ? AND status = 'open')
			AND NOT EXISTS (SELECT 1 FROM factory_attempt WHERE epic_id = ?)
			AND NOT EXISTS (SELECT 1 FROM factory_plan_gate WHERE epic_id = ? AND resolution NOT IN ('open', 'revision_requested'))
			AND EXISTS (SELECT 1 FROM factory_issue g WHERE g.epic_id = ? AND g.kind = 'gate' AND g.status = 'open'
				AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = g.id))`, proposal.EpicID, proposal.EpicID, proposal.EpicID, proposal.EpicID, proposal.EpicID)
		if err != nil {
			return model.NativeProposalRevision{}, false, err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed == 0 {
			return model.NativeProposalRevision{}, false, err
		}
	}
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
	if imported {
		details, err := json.Marshal(map[string]any{"revision": proposal.Revision, "contentHash": proposal.ContentHash})
		if err != nil {
			return model.NativeProposalRevision{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, actor, action, details_json, created_at) VALUES (?, 'agent', 'proposal.import', ?, ?)`, proposal.EpicID, string(details), proposal.CreatedAt); err != nil {
			return model.NativeProposalRevision{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_proposal_revision (epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, proposal.EpicID, proposal.MolID, proposal.Project, proposal.Revision, proposal.ManifestJSON, proposal.RationaleMarkdown, proposal.ContentHash, proposal.CreatedAt); err != nil {
		return model.NativeProposalRevision{}, false, fmt.Errorf("saving Factory proposal revision: %w", err)
	}
	var gateID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM factory_issue WHERE epic_id = ? AND kind = 'gate' ORDER BY id LIMIT 1`, proposal.EpicID).Scan(&gateID); err == nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(epic_id) DO UPDATE SET proposal_revision = excluded.proposal_revision, proposal_hash = excluded.proposal_hash, outcome = '', resolution = 'open', feedback = '', implementation_model = '', review_issue_ids_json = '[]', updated_at = excluded.updated_at`, proposal.EpicID, gateID, proposal.Revision, proposal.ContentHash, proposal.CreatedAt); err != nil {
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
	return scanFactoryPlanGate(d.db.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json, implementation_model FROM factory_plan_gate WHERE epic_id = ?`, epicID))
}

func (d *DB) GetFactoryProjectRequestGateForPlan(ctx context.Context, planID string) (model.ProjectRequestGate, bool, error) {
	var gateID string
	err := d.db.QueryRowContext(ctx, `SELECT issue_id FROM factory_project_request_gate WHERE plan_issue_id = ? AND resolution = 'approved'`, planID).Scan(&gateID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProjectRequestGate{}, false, nil
	}
	if err != nil {
		return model.ProjectRequestGate{}, false, err
	}
	gate, err := d.getFactoryProjectRequestGate(ctx, gateID)
	return gate, err == nil, err
}

// ApplyFactoryScopePlan appends newly discovered work and makes it block the
// interrupted Issue. Existing Issues and successful Attempt checkpoints are
// never rewritten.
func (d *DB) ApplyFactoryScopePlan(ctx context.Context, proposal model.NativeProposalRevision, attemptID, token string, at time.Time) (model.NativeProposalRevision, error) {
	var manifest struct {
		EpicID, MolID, Project string
		Nodes                  []struct {
			Key, Type, Requirement, Title, Description, Project string
			DependsOn                                           []string
		}
		Edges []struct{ From, To, Type string }
	}
	if err := json.Unmarshal([]byte(proposal.ManifestJSON), &manifest); err != nil {
		return model.NativeProposalRevision{}, errors.New("factory scope Plan manifest is invalid")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeProposalRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var planID, originalID string
	err = tx.QueryRowContext(ctx, `SELECT a.work_item_id, g.work_item_id FROM factory_attempt a JOIN factory_external_mapping m ON m.system = 'factory' AND m.external_kind = 'attempt_token' AND m.external_id = ? AND m.entity_kind = 'attempt' AND m.entity_id = a.id JOIN factory_project_request_gate g ON g.plan_issue_id = a.work_item_id AND g.resolution = 'approved' JOIN factory_issue original ON original.id = g.work_item_id AND original.status = 'deferred' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = original.id) WHERE a.id = ? AND a.epic_id = ? AND a.phase = 'active' AND json_extract(a.frozen_policy_json, '$.profile') = 'factory-plan/v1'`, token, attemptID, proposal.EpicID).Scan(&planID, &originalID)
	if err != nil {
		return model.NativeProposalRevision{}, errors.New("factory scope Plan attempt is unavailable")
	}
	var parentID string
	if err := tx.QueryRowContext(ctx, `SELECT parent_issue_id FROM factory_issue_hierarchy WHERE child_issue_id = ?`, planID).Scan(&parentID); err != nil || parentID != manifest.MolID {
		return model.NativeProposalRevision{}, errors.New("factory scope Plan manifest is invalid")
	}
	ids := make(map[string]string, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		var admitted bool
		if node.Type != "implementation" || node.Requirement == "reference" || tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_epic_project WHERE epic_id = ? AND project_path = ?)`, proposal.EpicID, node.Project).Scan(&admitted) != nil || !admitted {
			return model.NativeProposalRevision{}, errors.New("factory scope Plan manifest is invalid")
		}
		id, err := factoryChildID(ctx, tx, parentID)
		if err != nil {
			return model.NativeProposalRevision{}, err
		}
		ids[node.Key] = id
	}
	now := at.UnixMilli()
	for _, node := range manifest.Nodes {
		id := ids[node.Key]
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, 'implementation', ?, ?, 'open', ?)`, id, proposal.EpicID, node.Project, node.Title, node.Description, now); err != nil {
			return model.NativeProposalRevision{}, err
		}
		index, _ := factoryChildIndex(id)
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, ?)`, parentID, id, index, node.Requirement); err != nil {
			return model.NativeProposalRevision{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks')`, id, planID); err != nil {
			return model.NativeProposalRevision{}, err
		}
		if node.Requirement == "required" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks')`, originalID, id); err != nil {
				return model.NativeProposalRevision{}, err
			}
		}
	}
	edges := manifest.Edges
	for _, node := range manifest.Nodes {
		for _, dependency := range node.DependsOn {
			edges = append(edges, struct{ From, To, Type string }{node.Key, dependency, "blocks"})
		}
	}
	for _, edge := range edges {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, ?)`, ids[edge.From], ids[edge.To], edge.Type); err != nil {
			return model.NativeProposalRevision{}, err
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM factory_proposal_revision WHERE epic_id = ?`, proposal.EpicID).Scan(&proposal.Revision); err != nil {
		return model.NativeProposalRevision{}, err
	}
	proposal.CreatedAt = now
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_proposal_revision (epic_id, mol_id, project_path, revision, manifest_json, rationale_markdown, content_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, proposal.EpicID, proposal.MolID, proposal.Project, proposal.Revision, proposal.ManifestJSON, proposal.RationaleMarkdown, proposal.ContentHash, now); err != nil {
		return model.NativeProposalRevision{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = 'scope_replanned' WHERE id = ?`, planID); err != nil {
		return model.NativeProposalRevision{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'succeeded', finished_at = ?, updated_at = ? WHERE id = ?`, now, now, attemptID); err != nil {
		return model.NativeProposalRevision{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome_reason = '' WHERE id = ? AND status = 'deferred'`, originalID); err != nil {
		return model.NativeProposalRevision{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) VALUES (?, ?, ?, 'agent', 'project.replanned', json_object('proposalRevision', ?), ?)`, proposal.EpicID, originalID, attemptID, proposal.Revision, now); err != nil {
		return model.NativeProposalRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.NativeProposalRevision{}, err
	}
	return proposal, nil
}

func (d *DB) DecideFactoryPlanGate(ctx context.Context, epicID, action string, revision int, hash, feedback string, implementationModel ...string) (model.NativePlanGate, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativePlanGate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	gate, err := scanFactoryPlanGate(tx.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json, implementation_model FROM factory_plan_gate WHERE epic_id = ?`, epicID))
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
		if len(implementationModel) > 0 {
			gate.ImplementationModel = implementationModel[0]
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_plan_gate SET implementation_model = ? WHERE epic_id = ?`, gate.ImplementationModel, epicID); err != nil {
			return model.NativePlanGate{}, err
		}
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
	if err := scanner.Scan(&gate.EpicID, &gate.IssueID, &gate.ProposalRevision, &gate.ProposalHash, &gate.Outcome, &gate.Resolution, &gate.Feedback, &review, &gate.ImplementationModel); err != nil {
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
