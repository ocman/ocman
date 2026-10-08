package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// MutateFactoryGraph commits all edits and their approval revision together.
func (d *DB) MutateFactoryGraph(ctx context.Context, m model.GraphMutation) error {
	if m.Action == "approve_step" || m.Action == "reject_step" {
		return d.decideWorkflowStep(ctx, m)
	}
	changes := []model.GraphMutation{m}
	if m.Action == "batch" {
		if len(m.Mutations) == 0 || strings.TrimSpace(m.RationaleMarkdown) == "" {
			return fmt.Errorf("%w: batch requires mutations and rationaleMarkdown describing what changed and why", model.ErrInvalidGraphMutation)
		}
		changes = m.Mutations
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var pendingGraph bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_gate g JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ? AND g.resolution <> 'approved' AND json_type(p.manifest_json, '$.issues') = 'array')`, m.EpicID).Scan(&pendingGraph); err != nil {
		return err
	}
	requiresApproval := m.Actor == "mcp" || pendingGraph
	var baseline []model.NativeIssue
	if requiresApproval {
		baseline, err = factoryAmendmentBaselineTx(ctx, tx, m.EpicID)
		if err != nil {
			return err
		}
	}
	for _, change := range changes {
		if len(change.Mutations) != 0 || change.Action == "batch" || change.Action == "approve_step" || change.Action == "reject_step" || (change.EpicID != "" && change.EpicID != m.EpicID) {
			return fmt.Errorf("%w: batch edits must be structural changes in the same Epic", model.ErrInvalidGraphMutation)
		}
		change.EpicID, change.Actor = m.EpicID, m.Actor
		if err := mutateFactoryGraphTx(ctx, tx, change, requiresApproval); err != nil {
			return err
		}
	}
	if requiresApproval {
		if err := reopenFactoryGraphApprovalTx(ctx, tx, m.EpicID, baseline, m.RationaleMarkdown); err != nil {
			return err
		}
	}
	return tx.Commit()
}
