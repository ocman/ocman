package state

import (
	"context"
	"database/sql"
)

// A successful validator proves its frozen revision, not a later amendment.
const staleFactoryVerificationsSQL = `SELECT v.id FROM factory_issue v
	JOIN factory_workflow_step w ON w.issue_id = v.id AND json_extract(w.definition_json, '$.kind') = 'verification'
	JOIN factory_plan_gate g ON g.epic_id = v.epic_id AND g.resolution = 'approved'
	JOIN factory_proposal_revision p ON p.epic_id = g.epic_id AND p.revision = g.proposal_revision
	WHERE v.epic_id = ? AND v.kind = 'task' AND v.status = 'closed' AND v.outcome = 'succeeded'
	AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = v.id)
	AND json_type(p.manifest_json, '$.issues') = 'array'
	AND g.proposal_revision <> COALESCE((SELECT json_extract(a.frozen_policy_json, '$.planRevision') FROM factory_attempt a
		WHERE a.work_item_id = v.id AND a.terminal_outcome = 'succeeded' ORDER BY a.sequence DESC LIMIT 1), 0)`

func reopenStaleFactoryVerificationsTx(ctx context.Context, tx *sql.Tx, epicID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'open', outcome = '', outcome_reason = 'Graph revision requires fresh verification' WHERE id IN (`+staleFactoryVerificationsSQL+`)`, epicID)
	return err
}
