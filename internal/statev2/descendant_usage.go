package statev2

import (
	"context"
	"fmt"
)

// DescendantUsage reads indexed accounting for this subtree only. It neither
// loads parts nor visits ancestors or unrelated sessions with the same native ID.
func (d *DB) DescendantUsage(ctx context.Context, key Key) (map[string][]ConversationModelTotal, error) {
	if key.InstanceID == "" || key.SessionID == "" {
		return nil, fmt.Errorf("statev2: session key is required")
	}
	rows, err := d.reader().QueryContext(ctx, `WITH RECURSIVE children(id) AS (
		SELECT id FROM session WHERE instance_id=? AND id=?
		UNION SELECT s.id FROM session s JOIN children c ON s.parent_id=c.id WHERE s.instance_id=? AND s.source_deleted=0 AND COALESCE(json_extract(s.extensions,'$.SourceChild'),1)!=0)
		SELECT f.session_id,f.provider,f.model,sum(f.input_tokens),sum(f.output_tokens),sum(f.cache_read),sum(f.cache_write),
		sum(CASE WHEN f.cost>0 THEN f.cost ELSE 0 END),sum(CASE WHEN f.cost<=0 THEN f.cost ELSE 0 END)
		FROM analytics_fact f JOIN children c ON c.id=f.session_id WHERE f.instance_id=? AND f.role='assistant'
		GROUP BY f.session_id,f.provider,f.model,f.cost>0 ORDER BY f.session_id,f.provider,f.model,f.cost>0`, key.InstanceID, key.SessionID, key.InstanceID, key.InstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ConversationModelTotal{key.SessionID: {}}
	for rows.Next() {
		var id string
		var m ConversationModelTotal
		if err := rows.Scan(&id, &m.Provider, &m.Model, &m.Input, &m.Output, &m.CacheRead, &m.CacheWrite, &m.Paid, &m.Unpaid); err != nil {
			return nil, err
		}
		out[id] = append(out[id], m)
	}
	return out, rows.Err()
}
