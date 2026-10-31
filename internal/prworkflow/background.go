package prworkflow

import (
	"context"

	"github.com/ocman/ocman/internal/state"
)

// BackgroundCounts includes active watches and terminal notices awaiting delivery.
// These are hub-owned tasks, including when the session executes remotely.
func BackgroundCounts(ctx context.Context, db *state.DB) (map[state.Key]int, error) {
	counts := map[state.Key]int{}
	if db == nil {
		return counts, nil
	}
	rows, err := db.SQL().QueryContext(ctx, `SELECT platform, session_id, COUNT(*) FROM session_pull_request
		WHERE json_extract(payload, '$.watching') = 1 OR COALESCE(json_extract(payload, '$.Pending'), '') <> ''
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
