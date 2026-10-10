package state

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// factoryVerificationWaiverKind records, per verification Issue, the approved
// graph revision a human waived. A later revision still needs verification.
const factoryVerificationWaiverKind = "verification_waiver"

// DeliverFactoryEpicAsIs ends the review loop on the user's word: it accepts a
// pending graph revision, cancels the given attempts (stopped validators or
// paused work), leaves all unfinished implementation work out of the Epic as
// optional deferred Issues, and waives verification of the current revision,
// so the next dispatch runs delivery. It returns the deferred Issue IDs.
func (d *DB) DeliverFactoryEpicAsIs(ctx context.Context, epicID string, cancelAttempts []string, at time.Time) ([]string, error) {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", model.ErrInvalidGraphMutation, message) }
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := at.UnixMilli()
	result, err := tx.ExecContext(ctx, `UPDATE factory_epic SET status = 'open', updated_at = ? WHERE id = ? AND status IN ('open', 'paused')`, now, epicID)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, invalid("only an open or paused Epic can be delivered")
	}
	var gateIssue, resolution string
	var revision int
	var graph bool
	if err := tx.QueryRowContext(ctx, `SELECT g.issue_id, g.resolution, g.proposal_revision, COALESCE(json_type(p.manifest_json, '$.issues') = 'array', 0)
		FROM factory_plan_gate g LEFT JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision WHERE g.epic_id = ?`, epicID).Scan(&gateIssue, &resolution, &revision, &graph); err != nil {
		return nil, invalid("the Epic has no approved plan")
	}
	if resolution != "approved" {
		if !graph || resolution == "rejected" {
			return nil, invalid("approve the plan before delivering")
		}
		// The pending amendment's new work is left out with the rest below.
		if _, err := tx.ExecContext(ctx, `UPDATE factory_plan_gate SET outcome = 'succeeded', resolution = 'approved', feedback = 'Delivered as is', updated_at = ? WHERE epic_id = ?`, now, epicID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, gateIssue); err != nil {
			return nil, err
		}
		if err := closeHandBuiltMaterializationTx(ctx, tx, epicID); err != nil {
			return nil, err
		}
	}
	for _, id := range cancelAttempts {
		var workID string
		if err := tx.QueryRowContext(ctx, `SELECT work_item_id FROM factory_attempt WHERE id = ? AND epic_id = ? AND phase <> 'terminal'`, id, epicID).Scan(&workID); err != nil {
			return nil, invalid("a running attempt changed; try again")
		}
		// A decision already being carried out must finish first.
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_recovery_gate WHERE attempt_id = ? AND resolution NOT IN ('open', 'resume', 'retry', 'cancel'))
			OR EXISTS(SELECT 1 FROM factory_authority_escalation_gate WHERE attempt_id = ? AND resolution NOT IN ('open', 'approve', 'reject'))
			OR EXISTS(SELECT 1 FROM factory_project_request_gate WHERE attempt_id = ? AND resolution NOT IN ('open', 'approved', 'rejected'))`, id, id, id).Scan(&pending); err != nil {
			return nil, err
		}
		if pending {
			return nil, invalid("a decision on paused work is still being carried out; try again shortly")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'cancelled', finished_at = ?, updated_at = ? WHERE id = ?`, now, now, id); err != nil {
			return nil, err
		}
		// Every decision the cancelled attempt waited on is retired with it.
		for _, gate := range []struct{ table, resolution string }{{"factory_recovery_gate", "cancel"}, {"factory_authority_escalation_gate", "reject"}, {"factory_project_request_gate", "rejected"}} {
			if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'cancelled', outcome_reason = 'delivered_as_is' WHERE id IN (SELECT issue_id FROM `+gate.table+` WHERE attempt_id = ? AND resolution = 'open')`, id); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE `+gate.table+` SET resolution = ?, resolved_at = ? WHERE attempt_id = ? AND resolution = 'open'`, gate.resolution, now, id); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_recovery_gate SET response = 'Delivered as is' WHERE attempt_id = ? AND resolution = 'cancel' AND resolved_at = ?`, id, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open' WHERE id = ? AND status = 'in_progress'`, workID); err != nil {
			return nil, err
		}
	}
	var busy string
	err = tx.QueryRowContext(ctx, `SELECT i.title FROM factory_attempt a JOIN factory_issue i ON i.id = a.work_item_id WHERE a.epic_id = ? AND a.phase <> 'terminal' LIMIT 1`, epicID).Scan(&busy)
	if err == nil {
		return nil, invalid("wait for running work to finish: " + busy)
	}
	issues, err := listFactoryIssues(ctx, tx, epicID)
	if err != nil {
		return nil, err
	}
	completed := false
	var deferred []string
	for _, issue := range issues {
		step := ""
		if issue.Workflow != nil {
			step = issue.Workflow.Kind
		}
		switch {
		case step == "verification" && issue.Kind == "task":
			if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'succeeded', outcome_reason = 'Verification waived: delivered as is' WHERE id = ?`, issue.ID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO factory_external_mapping (system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at) VALUES ('factory', ?, ?, 'issue', ?, '{}', ?)
				ON CONFLICT (system, external_kind, entity_kind, entity_id) DO UPDATE SET external_id = excluded.external_id, created_at = excluded.created_at`, factoryVerificationWaiverKind, issue.ID+"@"+strconv.Itoa(revision), issue.ID, now); err != nil {
				return nil, err
			}
		case (issue.Kind == "task" || issue.Kind == "implementation") && (step == "" || step == "implementation") && issue.Requirement != "reference":
			if issue.Status == "closed" && issue.Outcome == "succeeded" {
				completed = true
				continue
			}
			if issue.Status == "deferred" && issue.OutcomeReason == model.DeliveredAsIsReason {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE factory_issue_hierarchy SET requirement = 'optional' WHERE child_issue_id = ? AND requirement = 'required'`, issue.ID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'deferred', outcome = '', outcome_reason = ?, retry_at = 0 WHERE id = ?`, model.DeliveredAsIsReason, issue.ID); err != nil {
				return nil, err
			}
			deferred = append(deferred, issue.ID)
		}
	}
	if !completed {
		return nil, invalid("no completed work to deliver")
	}
	details := `{"deferred":"` + strings.Join(deferred, ",") + `"}`
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at) VALUES (?, '', 'user', 'epic.deliver_as_is', ?, ?)`, epicID, details, now); err != nil {
		return nil, err
	}
	return deferred, tx.Commit()
}
