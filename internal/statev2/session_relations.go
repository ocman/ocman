package statev2

import (
	"context"
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
