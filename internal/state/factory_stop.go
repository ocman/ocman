package state

import (
	"context"
	"database/sql"
)

// Preserve completed work and partial files; cancellation is never success.
func cancelFactoryEpicTx(ctx context.Context, tx *sql.Tx, epicID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'cancelled', finished_at = ?, updated_at = ? WHERE epic_id = ? AND phase <> 'terminal'`, now, now, epicID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_issue SET status = 'closed', outcome = 'cancelled', outcome_reason = 'Epic stopped by user; unfinished work cancelled', retry_at = 0 WHERE epic_id = ? AND status <> 'closed'`, epicID); err != nil {
		return err
	}
	for _, gate := range []struct{ table, resolution, terminal string }{
		{"factory_recovery_gate", "cancel", "'resume', 'retry', 'cancel'"},
		{"factory_authority_escalation_gate", "reject", "'approve', 'reject'"},
		{"factory_project_request_gate", "rejected", "'approved', 'rejected'"},
	} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+gate.table+` SET resolution = ?, resolved_at = ? WHERE epic_id = ? AND resolution NOT IN (`+gate.terminal+`)`, gate.resolution, now, epicID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_plan_gate SET resolution = 'rejected', outcome = 'cancelled', feedback = 'Epic stopped by user', updated_at = ? WHERE epic_id = ? AND resolution NOT IN ('approved', 'rejected')`, now, epicID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inbox_item SET archived_at = ? WHERE substr(id, 1, ?) = ? AND archived_at IS NULL`, now, len("factory-action-"+epicID+":"), "factory-action-"+epicID+":"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_external_mapping SET metadata_json = json_set(metadata_json, '$.status', 'dismissed', '$.result', 'Epic stopped by user') WHERE system = 'factory' AND external_kind = ? AND json_extract(metadata_json, '$.epicId') = ? AND json_extract(metadata_json, '$.status') = 'pending'`, factoryControlActionKind, epicID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at) VALUES (?, '', 'user', 'epic.stop', '{"unfinishedWork":"cancelled","delivery":"not_requested"}', ?)`, epicID, now)
	return err
}
