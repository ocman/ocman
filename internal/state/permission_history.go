package state

import (
	"context"
	"encoding/json"
	"strings"
)

type PermissionHistoryEntry struct {
	InboxPermission
	RequestedAt int64  `json:"requestedAt"`
	ResolvedAt  int64  `json:"resolvedAt,omitempty"`
	Resolution  string `json:"resolution"`
}

// SessionPermissionHistory includes pending, rejected and one-time requests,
// not just approvals eligible for inheritance. Older approvals supplement the
// Inbox audit, which was introduced later.
func (d *DB) SessionPermissionHistory(ctx context.Context, platform string, sessionIDs []string) ([]PermissionHistoryEntry, error) {
	out := []PermissionHistoryEntry{}
	if len(sessionIDs) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(sessionIDs)), ",")
	args := []any{platform}
	for _, id := range sessionIDs {
		args = append(args, id)
	}
	args = append(args, args...)
	rows, err := d.db.QueryContext(ctx, `
		WITH requests AS (
			SELECT permission_json AS payload, created_at AS requested_at,
				COALESCE(archived_at, 0) AS resolved_at
			FROM inbox_item
			WHERE category = 'permission' AND permission_json != ''
			AND json_extract(permission_json, '$.platform') = ?
			AND json_extract(permission_json, '$.sessionId') IN (`+marks+`)
			UNION ALL
			SELECT json_object('platform', platform, 'sessionId', session_id,
				'permissionId', permission_id, 'permission', permission_text,
				'patterns', json(patterns_json), 'metadata', json(metadata_json)),
				CASE WHEN asked_at > 0 THEN asked_at ELSE approved_at END, approved_at
			FROM auto_approved_permission
			WHERE platform = ? AND session_id IN (`+marks+`)
		)
		SELECT r.payload, r.requested_at,
			MAX(r.resolved_at, COALESCE(l.resolved_at, 0), COALESCE(a.approved_at, 0)),
			COALESCE(NULLIF(l.resolution, ''), CASE WHEN a.permission_id IS NOT NULL
				THEN a.approved_by || '-' || a.reply
				WHEN r.resolved_at > 0 THEN 'resolved' ELSE 'pending' END)
		FROM requests r
		LEFT JOIN permission_lifecycle l ON l.platform = json_extract(r.payload, '$.platform')
			AND l.session_id = json_extract(r.payload, '$.sessionId')
			AND l.permission_id = json_extract(r.payload, '$.permissionId')
		LEFT JOIN auto_approved_permission a ON a.platform = json_extract(r.payload, '$.platform')
			AND a.session_id = json_extract(r.payload, '$.sessionId')
			AND a.permission_id = json_extract(r.payload, '$.permissionId')
		ORDER BY r.requested_at DESC LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var entry PermissionHistoryEntry
		var payload string
		if err := rows.Scan(&payload, &entry.RequestedAt, &entry.ResolvedAt, &entry.Resolution); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &entry.InboxPermission); err != nil {
			return nil, err
		}
		if entry.Patterns == nil {
			entry.Patterns = []string{}
		}
		key := entry.SessionID + "\x00" + entry.PermissionID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out, rows.Err()
}
