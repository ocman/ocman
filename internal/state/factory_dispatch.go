package state

import (
	"context"
	"database/sql"
	"time"
)

// NextFactoryDispatchAt preserves retry backoff and forge merge checks without
// scanning the whole Factory graph between state changes.
func (d *DB) NextFactoryDispatchAt(ctx context.Context, mergeInterval time.Duration) (time.Time, error) {
	var next sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT MIN(due) FROM (
		SELECT i.retry_at AS due FROM factory_issue i JOIN factory_epic e ON e.id = i.epic_id
		WHERE i.status = 'retry_wait' AND e.status = 'open'
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id = i.id)
		UNION ALL
		SELECT COALESCE(o.observed_at + ?, 0) AS due FROM factory_issue_dependency dep
		JOIN factory_issue delivery ON delivery.id = dep.depends_on_issue_id AND delivery.kind = 'delivery'
		JOIN factory_attempt a ON a.work_item_id = delivery.id AND a.terminal_outcome = 'succeeded'
		LEFT JOIN factory_merge_gate_observation o ON o.delivery_issue_id = delivery.id
		WHERE dep.type = 'merge_gated' AND delivery.status = 'closed' AND delivery.outcome = 'succeeded'
		AND json_extract(a.result_json, '$.prUrl') <> '' AND (o.status IS NULL OR o.status <> 'merged')
		AND NOT EXISTS (SELECT 1 FROM factory_removed_issue WHERE issue_id IN (dep.issue_id, delivery.id))
		AND NOT EXISTS (SELECT 1 FROM factory_attempt newer WHERE newer.work_item_id = delivery.id AND newer.terminal_outcome = 'succeeded' AND (newer.finished_at > a.finished_at OR (newer.finished_at = a.finished_at AND newer.rowid > a.rowid)))
	)`, mergeInterval.Milliseconds()).Scan(&next)
	if err != nil || !next.Valid {
		return time.Time{}, err
	}
	return time.UnixMilli(next.Int64), nil
}
