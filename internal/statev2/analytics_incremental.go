package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
)

// summaryScope limits incremental work to named messages and their old/new
// UTC hours. Full snapshot repairs use an unrestricted scope.
type summaryScope struct {
	facts, parts, hours  string
	args, hourArgs       []any
	users, input, output int64
	cost                 float64
}

func analyticsScope(ctx context.Context, tx *sql.Tx, key Key, ids []string) (summaryScope, error) {
	s := summaryScope{args: []any{key.InstanceID, key.SessionID}, hourArgs: []any{key.InstanceID, key.SessionID}}
	if ids == nil {
		return s, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return s, err
	}
	s.facts = ` AND id IN (SELECT value FROM json_each(?))`
	s.parts = ` AND message_id IN (SELECT value FROM json_each(?))`
	s.args = append(s.args, string(raw))
	// Carry forward only totals outside the changed set, never scan old facts.
	err = tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT user_messages FROM analytics_session WHERE instance_id=? AND id=?),0)-COALESCE(sum(role='user'),0),
		COALESCE((SELECT input_tokens FROM analytics_session WHERE instance_id=? AND id=?),0)-COALESCE(sum(CASE WHEN role='assistant' THEN input_tokens ELSE 0 END),0),
		COALESCE((SELECT output_tokens FROM analytics_session WHERE instance_id=? AND id=?),0)-COALESCE(sum(CASE WHEN role='assistant' THEN output_tokens ELSE 0 END),0),
		COALESCE((SELECT cost FROM analytics_session WHERE instance_id=? AND id=?),0)-COALESCE(sum(CASE WHEN role='assistant' THEN cost ELSE 0 END),0)
		FROM analytics_fact WHERE instance_id=? AND session_id=?`+s.facts,
		key.InstanceID, key.SessionID, key.InstanceID, key.SessionID, key.InstanceID, key.SessionID, key.InstanceID, key.SessionID,
		key.InstanceID, key.SessionID, string(raw)).Scan(&s.users, &s.input, &s.output, &s.cost)
	if err != nil {
		return s, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT (time_created/3600000)*3600000 FROM analytics_fact WHERE instance_id=? AND session_id=?`+s.facts+
		` UNION SELECT DISTINCT (time_created/3600000)*3600000 FROM analytics_message WHERE instance_id=? AND session_id=?`+s.facts,
		append(append([]any{}, s.args...), s.args...)...)
	if err != nil {
		return s, err
	}
	var buckets []int64
	for rows.Next() {
		var bucket int64
		if err := rows.Scan(&bucket); err != nil {
			rows.Close()
			return s, err
		}
		buckets = append(buckets, bucket)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	raw, err = json.Marshal(buckets)
	if err != nil {
		return s, err
	}
	s.hours = ` AND bucket IN (SELECT value FROM json_each(?))`
	s.hourArgs = append(s.hourArgs, string(raw))
	return s, nil
}

func changedAnalyticsMessages(b StreamBatch) []string {
	seen := map[string]bool{}
	ids := []string{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, m := range b.Messages {
		add(m.ID)
	}
	for _, p := range b.Parts {
		add(p.MessageID)
	}
	for _, id := range b.RemovedMessages {
		add(id)
	}
	for _, p := range b.RemovedParts {
		add(p.MessageID)
	}
	return ids
}
