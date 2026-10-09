package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

const factoryVerificationActor = "verification"

// AddFactoryVerificationFollowUps lets an active validator turn the gaps it
// found into Epic tickets without a graph-approval round: nearly every such
// approval was a rubber stamp. The new work reopens the implementation phase
// and the verification step goes back to open behind it, so a fresh
// verification runs once the follow-ups land. Only new Issues are allowed;
// the user can still transfer or delete them like any open ticket.
func (d *DB) AddFactoryVerificationFollowUps(ctx context.Context, attemptID string, m model.GraphMutation, at time.Time) ([]string, error) {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	changes := []model.GraphMutation{m}
	if m.Action == "batch" {
		changes = m.Mutations
	}
	rationale := strings.TrimSpace(m.RationaleMarkdown)
	if len(changes) == 0 || rationale == "" {
		return nil, invalid("follow-ups require mutations and rationaleMarkdown describing the failed criteria")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var workID, epicID string
	err = tx.QueryRowContext(ctx, `SELECT a.work_item_id, a.epic_id FROM factory_attempt a JOIN factory_workflow_step w ON w.issue_id = a.work_item_id
		WHERE a.id = ? AND a.phase = 'active' AND json_extract(w.definition_json, '$.kind') = 'verification'`, attemptID).Scan(&workID, &epicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalid("only an active verification attempt may add follow-up Issues")
	}
	if err != nil {
		return nil, err
	}
	if m.EpicID != "" && m.EpicID != epicID {
		return nil, invalid("follow-ups must target the verified Epic")
	}
	var pendingGraph bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_gate g JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ? AND g.resolution <> 'approved' AND json_type(p.manifest_json, '$.issues') = 'array')`, epicID).Scan(&pendingGraph); err != nil {
		return nil, err
	}
	if pendingGraph {
		return nil, invalid("a graph revision awaits human approval; call request_recovery instead")
	}
	var created []string
	for _, change := range changes {
		if change.Action != "create" || (change.Kind != "task" && change.Kind != "implementation") || len(change.Mutations) != 0 || (change.EpicID != "" && change.EpicID != epicID) {
			return nil, invalid("verification follow-ups may only create task or implementation Issues")
		}
		change.EpicID, change.Actor = epicID, factoryVerificationActor
		if err := mutateFactoryGraphTx(ctx, tx, change, false); err != nil {
			return nil, err
		}
		var id string // the row mutateFactoryGraphTx just inserted
		if err := tx.QueryRowContext(ctx, `SELECT id FROM factory_issue WHERE epic_id = ? ORDER BY rowid DESC LIMIT 1`, epicID).Scan(&id); err != nil {
			return nil, err
		}
		created = append(created, id)
	}
	now := at.UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'cancelled', finished_at = ?, updated_at = ? WHERE id = ? AND phase = 'active'`, now, now, attemptID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '', outcome_reason = '' WHERE id = ?`, workID); err != nil {
		return nil, err
	}
	body := "Verification added follow-up Issues " + strings.Join(created, ", ") + " and will run again after them.\n\n" + rationale
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_issue_comment (issue_id, actor, body, created_at) VALUES (?, ?, ?, ?)`, workID, factoryVerificationActor, body, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, attempt_id, actor, action, details_json, created_at) VALUES (?, ?, ?, 'agent', 'verification.follow_ups', json_object('issues', ?), ?)`, epicID, workID, attemptID, strings.Join(created, ","), now); err != nil {
		return nil, err
	}
	return created, tx.Commit()
}
