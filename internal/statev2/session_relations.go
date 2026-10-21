package statev2

import (
	"context"
	"encoding/json"
	"fmt"
)

func (d *DB) SessionParents(ctx context.Context, instance string) (map[string]string, error) {
	if instance == "" {
		return nil, fmt.Errorf("statev2: session instance is required")
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT id,CASE WHEN COALESCE(json_extract(extensions,'$.SourceChild'),1)=0 THEN '' ELSE parent_id END FROM session WHERE instance_id=? AND source_deleted=0`, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		out[id] = parent
	}
	return out, rows.Err()
}

func (d *DB) Descendants(ctx context.Context, key Key) ([]string, error) {
	if key.InstanceID == "" || key.SessionID == "" {
		return nil, fmt.Errorf("statev2: session key is required")
	}
	rows, err := d.reader().QueryContext(ctx, `WITH RECURSIVE children(id) AS (
		SELECT id FROM session WHERE instance_id=? AND id=?
		UNION SELECT s.id FROM session s JOIN children c ON s.parent_id=c.id WHERE s.instance_id=? AND s.source_deleted=0 AND COALESCE(json_extract(s.extensions,'$.SourceChild'),1)!=0
		) SELECT id FROM children ORDER BY (id=?) DESC,id`, key.InstanceID, key.SessionID, key.InstanceID, key.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UnreadCounts batches indexed ranges and keeps native message IDs irrelevant to
// identity. Missing or deleted sessions never contribute counts.
func (d *DB) UnreadCounts(ctx context.Context, instance string, cutoffs map[string]int64) (map[string]int, error) {
	if instance == "" {
		return nil, fmt.Errorf("statev2: session instance is required")
	}
	if len(cutoffs) == 0 {
		return map[string]int{}, nil
	}
	raw, err := json.Marshal(cutoffs)
	if err != nil {
		return nil, err
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT c.key,(SELECT count(*) FROM message m WHERE m.instance_id=? AND m.session_id=c.key AND m.source_deleted=0 AND m.created_at>c.value)
		FROM json_each(?) c JOIN session s ON s.instance_id=? AND s.id=c.key WHERE s.source_deleted=0`, instance, string(raw), instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		if count > 0 {
			out[id] = count
		}
	}
	return out, rows.Err()
}
