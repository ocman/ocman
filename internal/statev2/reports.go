package statev2

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// The report queries mirror the analytics readers so the two can be compared
// before any reader moves here. Sessions removed from the source are retained
// in this store but excluded, because the source no longer counts them.

// DayActivity is one local calendar day of activity.
type DayActivity struct {
	Day          string
	Sessions     int64 // root sessions created that day
	Messages     int64 // assistant messages, including subagents
	UserMessages int64 // user prompts in root sessions
}

// DailyActivity totals an instance's activity per local day since the given
// time in unix milliseconds, oldest first. Days without activity are omitted.
func (d *DB) DailyActivity(ctx context.Context, instanceID string, since int64) ([]DayActivity, error) {
	days := map[string]*DayActivity{}
	var order []string
	add := func(query string, set func(*DayActivity, int64)) error {
		rows, err := d.db.QueryContext(ctx, query, instanceID, since)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day string
			var n int64
			if err := rows.Scan(&day, &n); err != nil {
				return err
			}
			if days[day] == nil {
				days[day] = &DayActivity{Day: day}
				order = append(order, day)
			}
			set(days[day], n)
		}
		return rows.Err()
	}
	if err := add(`SELECT date(s.created_at / 1000, 'unixepoch', 'localtime') AS day, count(*)
		FROM session s WHERE s.instance_id=? AND s.created_at>=? AND s.source_deleted=0
		AND s.is_subagent=0 GROUP BY day`,
		func(a *DayActivity, n int64) { a.Sessions = n }); err != nil {
		return nil, err
	}
	if err := add(`SELECT date(m.created_at / 1000, 'unixepoch', 'localtime') AS day, count(*)
		FROM message m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
		WHERE m.instance_id=? AND m.role='assistant' AND m.created_at>=? AND m.source_deleted=0 AND s.source_deleted=0
		GROUP BY day`,
		func(a *DayActivity, n int64) { a.Messages = n }); err != nil {
		return nil, err
	}
	if err := add(`SELECT date(m.created_at / 1000, 'unixepoch', 'localtime') AS day, count(*)
		FROM message m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
		WHERE m.instance_id=? AND m.role='user' AND m.created_at>=? AND m.source_deleted=0 AND s.source_deleted=0
		AND s.is_subagent=0 GROUP BY day`,
		func(a *DayActivity, n int64) { a.UserMessages = n }); err != nil {
		return nil, err
	}
	sort.Strings(order) // ISO dates sort chronologically
	out := make([]DayActivity, 0, len(days))
	for _, key := range order {
		out = append(out, *days[key])
	}
	return out, nil
}

// ModelUsageRow totals assistant messages for one "provider/model" key.
type ModelUsageRow struct {
	Key        string
	Count      int64
	TokensIn   int64
	TokensOut  int64
	CacheRead  int64
	CacheWrite int64
}

// ModelUsage totals assistant messages per model since the given time,
// ordered by key. Messages without a model are skipped, as in analytics.
func (d *DB) ModelUsage(ctx context.Context, instanceID string, since int64) ([]ModelUsageRow, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT m.model, count(*), sum(m.tokens_input), sum(m.tokens_output),
		sum(m.tokens_cache_read), sum(m.tokens_cache_write)
		FROM message m JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
		WHERE m.instance_id=? AND m.role='assistant' AND m.created_at>=? AND m.model<>''
		AND m.source_deleted=0 AND s.source_deleted=0
		GROUP BY m.model ORDER BY m.model`, instanceID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelUsageRow{}
	for rows.Next() {
		var r ModelUsageRow
		if err := rows.Scan(&r.Key, &r.Count, &r.TokensIn, &r.TokensOut, &r.CacheRead, &r.CacheWrite); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InstanceBySource finds the instance registered for one physical source.
func (d *DB) InstanceBySource(ctx context.Context, ownerID, platform, sourceKey string) (Instance, error) {
	i := Instance{OwnerID: ownerID, Platform: platform, SourceKey: sourceKey}
	err := d.db.QueryRowContext(ctx, `SELECT id,name FROM instance WHERE owner_id=? AND platform=? AND source_key=?`,
		ownerID, platform, sourceKey).Scan(&i.ID, &i.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, ErrNotFound
	}
	return i, err
}
