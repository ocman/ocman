package state

import (
	"context"
	"time"
)

// FactoryAttemptLastResumedAt is when a human last resolved any pause gate
// on the attempt. The idle watchdog measures from here, so a resumed
// attempt is not judged by the silence that preceded its pause.
func (d *DB) FactoryAttemptLastResumedAt(ctx context.Context, attemptID string) (time.Time, error) {
	var at int64
	err := d.db.QueryRowContext(ctx, `SELECT MAX(
		COALESCE((SELECT MAX(resolved_at) FROM factory_recovery_gate WHERE attempt_id = ?1), 0),
		COALESCE((SELECT MAX(resolved_at) FROM factory_authority_escalation_gate WHERE attempt_id = ?1), 0),
		COALESCE((SELECT MAX(resolved_at) FROM factory_project_request_gate WHERE attempt_id = ?1), 0))`, attemptID).Scan(&at)
	if err != nil || at == 0 {
		return time.Time{}, err
	}
	return time.UnixMilli(at), nil
}
