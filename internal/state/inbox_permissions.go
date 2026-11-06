package state

import (
	"context"
	"encoding/json"
)

// PendingInboxPermissions reads only actionable permission payloads. Display
// bodies and session context are not needed by the recovery sweep.
func (d *DB) PendingInboxPermissions(ctx context.Context) ([]InboxPermission, error) {
	rows, err := d.reader().QueryContext(ctx, pendingInboxPermissionsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	permissions := []InboxPermission{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var permission *InboxPermission
		if err := json.Unmarshal([]byte(raw), &permission); err != nil {
			return nil, err
		}
		if permission != nil {
			permissions = append(permissions, *permission)
		}
	}
	return permissions, rows.Err()
}

const pendingInboxPermissionsSQL = `SELECT permission_json FROM inbox_item
	WHERE archived_at IS NULL AND category='permission' AND permission_json!=''
	ORDER BY created_at DESC,id DESC`
