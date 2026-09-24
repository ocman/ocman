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

// MaterializeFactoryPlan creates the approved implementation graph and closes
// its Materialization Issue in the same transaction.
func (d *DB) MaterializeFactoryPlan(ctx context.Context, epicID, issueID, profile string, at time.Time) (model.NativeMaterialization, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return model.NativeMaterialization{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var gate model.NativePlanGate
	gate, err = scanFactoryPlanGate(tx.QueryRowContext(ctx, `SELECT epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, feedback, review_issue_ids_json, implementation_model FROM factory_plan_gate WHERE epic_id = ?`, epicID))
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
		rows, err := tx.QueryContext(ctx, `SELECT manifest_key, entity_id FROM factory_materialization_provenance WHERE materialization_id = ? AND entity_kind = 'issue' ORDER BY rowid`, existing.ID)
		if err != nil {
			return model.NativeMaterialization{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var item model.NativeMaterializedIssue
			if err := rows.Scan(&item.ManifestKey, &item.IssueID); err != nil {
				return model.NativeMaterialization{}, err
			}
			existing.Issues = append(existing.Issues, item)
		}
		if err := rows.Err(); err != nil {
			return model.NativeMaterialization{}, err
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
	var workflow bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_workflow_step WHERE issue_id = ?)`, issueID).Scan(&workflow); err != nil {
		return model.NativeMaterialization{}, err
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
			Key         string   `json:"key"`
			Type        string   `json:"type"`
			Requirement string   `json:"requirement"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Project     string   `json:"project"`
			DependsOn   []string `json:"dependsOn"`
			Pinned      bool     `json:"pinned"`
		} `json:"nodes"`
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(proposal.ManifestJSON), &manifest); err != nil || manifest.EpicID != epicID || manifest.MolID != proposal.MolID || manifest.Project != proposal.Project {
		return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
	}
	executable := make([]int, 0, len(manifest.Nodes))
	deliveries := make([]int, 0, len(manifest.Nodes))
	deliveryProjects := map[string]bool{}
	implementationProjects := map[string]bool{}
	keys := make(map[string]int, len(manifest.Nodes))
	required := 0
	for i := range manifest.Nodes {
		node := &manifest.Nodes[i]
		if node.Project == "" {
			node.Project = manifest.Project
		}
		var admitted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_epic_project WHERE epic_id = ? AND project_path = ?)`, epicID, node.Project).Scan(&admitted); err != nil || !admitted {
			return model.NativeMaterialization{}, errors.New("factory approved Plan targets a project that is no longer admitted")
		}
		if !model.ValidNativeFormulaKey(node.Key) {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
		if _, exists := keys[node.Key]; exists || (node.Type != "implementation" && node.Type != "delivery") || (node.Requirement != "required" && node.Requirement != "optional" && node.Requirement != "reference") || (node.Pinned && node.Requirement != "reference") {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
		if node.Type == "delivery" && (node.Requirement != "required" || node.Pinned || len(node.DependsOn) != 0 || deliveryProjects[node.Project]) {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
		keys[node.Key] = len(keys)
		if node.Type == "implementation" && node.Requirement != "reference" {
			executable = append(executable, len(keys)-1)
			implementationProjects[node.Project] = true
		}
		if node.Type == "delivery" {
			deliveries = append(deliveries, len(keys)-1)
			deliveryProjects[node.Project] = true
		}
		if node.Type == "implementation" && node.Requirement == "required" {
			required++
		}
	}
	if required == 0 {
		return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
	}
	if workflow && len(deliveries) != 0 {
		return model.NativeMaterialization{}, errors.New("workflow delivery belongs in the Formula, not the implementation plan")
	}
	for project := range deliveryProjects {
		if !implementationProjects[project] {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
	}
	edges := manifest.Edges
	for _, index := range executable {
		for _, dependency := range manifest.Nodes[index].DependsOn {
			edges = append(edges, struct {
				From string `json:"from"`
				To   string `json:"to"`
				Type string `json:"type"`
			}{From: manifest.Nodes[index].Key, To: dependency, Type: "blocks"})
		}
	}
	seenEdges := map[string]bool{}
	for _, edge := range edges {
		from, fromOK := keys[edge.From]
		to, toOK := keys[edge.To]
		pair := edge.From + "\x00" + edge.To
		validType := edge.Type == "blocks" || edge.Type == "on_failure" || edge.Type == "merge_gated"
		validDelivery := fromOK && toOK && edge.Type == "merge_gated" && manifest.Nodes[from].Type == "implementation" && manifest.Nodes[to].Type == "delivery" && manifest.Nodes[from].Project != manifest.Nodes[to].Project
		if !fromOK || !toOK || manifest.Nodes[from].Requirement == "reference" || manifest.Nodes[to].Requirement == "reference" || !validType || (edge.Type == "merge_gated" && !validDelivery) || (edge.Type != "merge_gated" && (manifest.Nodes[from].Type == "delivery" || manifest.Nodes[to].Type == "delivery")) || seenEdges[pair] {
			return model.NativeMaterialization{}, errors.New("factory approved Plan manifest is invalid")
		}
		seenEdges[pair] = true
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
	issueIDs := make(map[string]string, len(executable)+len(deliveries))
	reusedDeliveries := map[string]bool{}
	for _, index := range append(executable, deliveries...) {
		if manifest.Nodes[index].Type == "delivery" {
			var existingID, status, outcome string
			var merged bool
			err := tx.QueryRowContext(ctx, `SELECT id, status, outcome, EXISTS(SELECT 1 FROM factory_merge_gate_observation WHERE delivery_issue_id = factory_issue.id AND status = 'merged') FROM factory_issue WHERE epic_id = ? AND project_path = ? AND kind = 'delivery' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = factory_issue.id) ORDER BY created_at DESC, id DESC LIMIT 1`, epicID, manifest.Nodes[index].Project).Scan(&existingID, &status, &outcome, &merged)
			if err == nil {
				if status == "in_progress" {
					return model.NativeMaterialization{}, errors.New("factory approved Plan cannot replace a started Project Delivery")
				}
				if status != "closed" || outcome != "succeeded" || !merged {
					issueIDs[manifest.Nodes[index].Key], reusedDeliveries[existingID] = existingID, true
					continue
				}
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return model.NativeMaterialization{}, err
			}
		}
		parent := proposal.MolID
		if workflow && manifest.Nodes[index].Type == "implementation" {
			parent = issueID
		}
		implementationID, err := factoryChildID(ctx, tx, parent)
		if err != nil {
			return model.NativeMaterialization{}, err
		}
		issueIDs[manifest.Nodes[index].Key] = implementationID
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_materialization m JOIN factory_materialization_provenance p ON p.materialization_id = m.id AND p.entity_kind = 'issue' JOIN factory_issue i ON i.id = p.entity_id WHERE m.epic_id = ? AND i.status = 'in_progress' AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)`, epicID).Scan(&active); err != nil {
		return model.NativeMaterialization{}, err
	}
	if active != 0 {
		return model.NativeMaterialization{}, errors.New("factory approved Plan cannot remove active implementation work")
	}
	// Superseded implementations take their descendants with them; an orphan
	// whose parent is removed would break the requirement walk in listings.
	if _, err := tx.ExecContext(ctx, `WITH RECURSIVE superseded(id) AS (
			SELECT p.entity_id FROM factory_materialization m JOIN factory_materialization_provenance p ON p.materialization_id = m.id AND p.entity_kind = 'issue' JOIN factory_issue i ON i.id = p.entity_id AND i.kind <> 'delivery' WHERE m.epic_id = ?
			UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN superseded s ON h.parent_issue_id = s.id JOIN factory_issue child ON child.id = h.child_issue_id AND child.kind <> 'delivery')
		INSERT INTO factory_removed_issue (issue_id, plan_id, plan_revision, removed_at)
		SELECT id, ?, ?, ? FROM superseded WHERE true
		ON CONFLICT(issue_id) DO NOTHING`, epicID, epicID, proposal.Revision, now); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("removing superseded Factory implementation: %w", err)
	}
	primary := manifest.Nodes[executable[0]]
	primaryID := issueIDs[primary.Key]
	result := model.NativeMaterialization{ID: id, EpicID: epicID, IssueID: issueID, ProposalRevision: proposal.Revision, ProposalHash: proposal.ContentHash, ManifestKey: primary.Key, ImplementationID: primaryID}
	implementationParent := proposal.MolID
	if workflow {
		implementationParent = issueID
	}
	for _, index := range executable {
		node := manifest.Nodes[index]
		implementationID := issueIDs[node.Key]
		title, description := strings.TrimSpace(node.Title), strings.TrimSpace(node.Description)
		if title == "" {
			title = "Implementation: " + goal
		}
		if description == "" {
			description = proposal.RationaleMarkdown
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, 'implementation', ?, ?, 'open', ?)`, implementationID, epicID, node.Project, title, description, now); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("creating Factory implementation: %w", err)
		}
		implementationIndex, err := factoryChildIndex(implementationID)
		if err != nil {
			return model.NativeMaterialization{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, ?)`, implementationParent, implementationID, implementationIndex, node.Requirement); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("adding Factory implementation closure: %w", err)
		}
		dependencySQL := `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, 'blocks')`
		if workflow {
			dependencySQL = `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) SELECT ?, depends_on_issue_id, type FROM factory_issue_dependency WHERE issue_id = ?`
		}
		if _, err := tx.ExecContext(ctx, dependencySQL, implementationID, issueID); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("adding Factory implementation dependency: %w", err)
		}
		result.Issues = append(result.Issues, model.NativeMaterializedIssue{ManifestKey: node.Key, IssueID: implementationID})
	}
	for _, index := range deliveries {
		node := manifest.Nodes[index]
		deliveryID := issueIDs[node.Key]
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = "Deliver the completed work"
		}
		if reusedDeliveries[deliveryID] {
			if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET title = ?, description = ?, status = 'open', outcome = '', outcome_reason = '', retry_at = 0, retry_attempts = 0 WHERE id = ?`, title, strings.TrimSpace(node.Description), deliveryID); err != nil {
				return model.NativeMaterialization{}, fmt.Errorf("reusing Factory delivery: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM factory_merge_gate_observation WHERE delivery_issue_id = ?`, deliveryID); err != nil {
				return model.NativeMaterialization{}, fmt.Errorf("resetting Factory delivery observation: %w", err)
			}
			var parentID string
			if err := tx.QueryRowContext(ctx, `SELECT parent_issue_id FROM factory_issue_hierarchy WHERE child_issue_id = ?`, deliveryID).Scan(&parentID); err != nil {
				return model.NativeMaterialization{}, err
			}
			if parentID != proposal.MolID {
				indexedID, err := factoryChildID(ctx, tx, proposal.MolID)
				if err != nil {
					return model.NativeMaterialization{}, err
				}
				index, _ := factoryChildIndex(indexedID)
				if _, err := tx.ExecContext(ctx, `DELETE FROM factory_issue_hierarchy WHERE child_issue_id = ?`, deliveryID); err != nil {
					return model.NativeMaterialization{}, err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, proposal.MolID, deliveryID, index); err != nil {
					return model.NativeMaterialization{}, err
				}
			}
			result.Issues = append(result.Issues, model.NativeMaterializedIssue{ManifestKey: node.Key, IssueID: deliveryID})
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue (id, epic_id, project_path, kind, title, description, status, created_at) VALUES (?, ?, ?, 'delivery', ?, ?, 'open', ?)`, deliveryID, epicID, node.Project, title, strings.TrimSpace(node.Description), now); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("creating Factory delivery: %w", err)
		}
		index, _ := factoryChildIndex(deliveryID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, proposal.MolID, deliveryID, index); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("adding Factory delivery closure: %w", err)
		}
		result.Issues = append(result.Issues, model.NativeMaterializedIssue{ManifestKey: node.Key, IssueID: deliveryID})
	}
	for _, edge := range edges {
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_dependency (issue_id, depends_on_issue_id, type) VALUES (?, ?, ?)`, issueIDs[edge.From], issueIDs[edge.To], edge.Type); err != nil {
			return model.NativeMaterialization{}, fmt.Errorf("adding Factory implementation dependency: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_materialization (id, epic_id, issue_id, proposal_revision, proposal_hash, manifest_key, profile, implementation_issue_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, epicID, issueID, proposal.Revision, proposal.ContentHash, primary.Key, profile, primaryID, now); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("recording Factory materialization: %w", err)
	}
	for _, index := range append(executable, deliveries...) {
		node := manifest.Nodes[index]
		implementationID := issueIDs[node.Key]
		parent := proposal.MolID
		if node.Type == "implementation" {
			parent = implementationParent
		}
		entities := []struct{ kind, id string }{{"issue", implementationID}, {"hierarchy", parent + "\x00" + implementationID}}
		if node.Type == "implementation" && !workflow {
			entities = append(entities, struct{ kind, id string }{"dependency", implementationID + "\x00" + issueID})
		}
		for _, edge := range edges {
			if edge.From == node.Key {
				entities = append(entities, struct{ kind, id string }{"dependency", implementationID + "\x00" + issueIDs[edge.To]})
			}
		}
		for _, entity := range entities {
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_materialization_provenance (entity_kind, entity_id, plan_id, plan_revision, materialization_id, manifest_key) VALUES (?, ?, ?, ?, ?, ?)`, entity.kind, entity.id, epicID, proposal.Revision, id, node.Key); err != nil {
				return model.NativeMaterialization{}, fmt.Errorf("recording Factory materialization provenance: %w", err)
			}
		}
	}
	completionSQL := `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ? AND status = 'open'`
	if workflow {
		completionSQL = `UPDATE factory_issue SET kind = 'phase' WHERE id = ? AND status = 'open'`
	}
	if _, err := tx.ExecContext(ctx, completionSQL, issueID); err != nil {
		return model.NativeMaterialization{}, fmt.Errorf("closing Factory materialization: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.NativeMaterialization{}, err
	}
	return result, nil
}
