package prworkflow

import (
	"context"

	"github.com/ocman/ocman/internal/state"
)

// BackgroundCounts includes active watches and nonterminal updates awaiting delivery.
// A merged or closed PR must not keep its session in progress for a queued notice.
// Watches are owned by the same instance as their session. Legacy hub rows are
// transferred by the PR workflow before polling resumes.
func BackgroundCounts(ctx context.Context, db *state.DB) (map[state.Key]int, error) {
	counts := map[state.Key]int{}
	if db == nil {
		return counts, nil
	}
	rows, err := db.SQL().QueryContext(ctx, `SELECT platform, session_id, COUNT(*) FROM session_pull_request
		WHERE json_extract(payload, '$.watching') = 1 OR (COALESCE(json_extract(payload, '$.Pending'), '') <> ''
		AND COALESCE(json_extract(payload, '$.pr.status'), '') NOT IN ('merged', 'closed'))
		GROUP BY platform, session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key state.Key
		var count int
		if err := rows.Scan(&key.Platform, &key.SessionID, &count); err != nil {
			return nil, err
		}
		counts[key] = count
	}
	return counts, rows.Err()
}
