package state

import (
	"context"
	"database/sql"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// The blocker queries use alias b. A proposed removal remains effective until
// its source graph is approved, but approved removals never revive on later edits.
const effectiveFactoryBlockerSQL = `(NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = b.id)
	OR EXISTS (SELECT 1 FROM factory_removed_issue r JOIN factory_plan_gate source_gate ON source_gate.epic_id = r.plan_id
		JOIN factory_proposal_revision source_plan ON source_plan.epic_id = source_gate.epic_id AND source_plan.revision = source_gate.proposal_revision
		WHERE r.issue_id = b.id AND r.plan_revision > 0 AND r.plan_revision <= source_gate.proposal_revision
		AND source_gate.resolution <> 'approved' AND json_type(source_plan.manifest_json, '$.issues') = 'array'
		AND r.plan_revision > COALESCE(json_extract(source_plan.manifest_json, '$.baseRevision'), 0)))`

func removeFactoryGraphSubtreeTx(ctx context.Context, tx *sql.Tx, mutation model.GraphMutation, requiresApproval bool) error {
	revision := 0
	if requiresApproval {
		// The snapshot allocator runs later in this same transaction.
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM factory_proposal_revision WHERE epic_id = ?`, mutation.EpicID).Scan(&revision); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT ? UNION ALL SELECT h.child_issue_id FROM factory_issue_hierarchy h JOIN descendants d ON h.parent_issue_id = d.id)
		INSERT INTO factory_removed_issue (issue_id, plan_id, plan_revision, removed_at) SELECT id, epic_id, ?, ? FROM factory_issue WHERE id IN (SELECT id FROM descendants) ON CONFLICT(issue_id) DO NOTHING`, mutation.IssueID, revision, time.Now().UnixMilli())
	return err
}
