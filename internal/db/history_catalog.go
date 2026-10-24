package db

import (
	"context"
	"database/sql"
	"errors"
)

// GetRecentModels samples the latest sessions in retained history.
//
// Catalog preferences use compact scalar facts, not retained message JSON.
// Both queries stay on the scope's validated transaction and join full source
// identities so matching native IDs cannot borrow another source's defaults.
func (d *DB) GetRecentModels(ctx context.Context, sessionLimit, maxResults int) ([]RecentModel, error) {
	if sessionLimit <= 0 {
		sessionLimit = 50
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	rows, err := d.reads().QueryContext(ctx, `WITH candidates AS (
	 SELECT instance_id,id FROM session ORDER BY time_updated DESC,id LIMIT ?
	) SELECT f.provider,f.model,max(f.time_created) FROM candidates c
	 CROSS JOIN history_fact f ON f.instance_id=c.instance_id AND f.session_id=c.id
	 WHERE f.role='assistant' AND f.model<>''
	 GROUP BY f.provider,f.model ORDER BY max(f.time_created) DESC,f.provider,f.model LIMIT ?`, sessionLimit, maxResults)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecentModel{}
	for rows.Next() {
		var row RecentModel
		if err := rows.Scan(&row.Provider, &row.Model, &row.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (d *DB) historySessionDefaults(ctx context.Context, exclude, directory string) (SessionDefaults, bool, error) {
	where := ""
	args := []any{exclude}
	if directory != "" {
		where = " AND directory=?"
		args = append(args, directory)
	}
	var defaults SessionDefaults
	err := d.reads().QueryRowContext(ctx, `WITH candidates AS (
	 SELECT instance_id,id,time_updated FROM session
	 WHERE id<>? AND title NOT LIKE '%(% subagent)'`+where+`
	 ORDER BY time_updated DESC,id LIMIT 5
	) SELECT f.agent,CASE WHEN f.provider<>'' AND f.model<>'' THEN f.provider||'/'||f.model ELSE f.model END
	 FROM candidates c CROSS JOIN history_fact f ON f.instance_id=c.instance_id AND f.session_id=c.id
	 WHERE f.role='assistant' AND (f.agent<>'' OR f.provider<>'' OR f.model<>'')
	 ORDER BY c.time_updated DESC,f.time_created DESC,f.id LIMIT 1`, args...).Scan(&defaults.Agent, &defaults.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return defaults, false, nil
	}
	return defaults, err == nil, err
}
