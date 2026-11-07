package state

import (
	"context"
	"encoding/json"
)

// SidebarArchives reads only archive preferences for the watched rows. Reading
// every seen/pin/project preference to hold a handful of PR watches is O(history).
func (d *DB) SidebarArchives(ctx context.Context, keys []Key) (map[Key]int64, string, error) {
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, "", err
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT a.platform,a.session_id,a.session_time_updated,''
	 FROM json_each(?) k CROSS JOIN archived_session a
	 ON a.platform=json_extract(k.value,'$.Platform') AND a.session_id=json_extract(k.value,'$.SessionID')
	 UNION ALL SELECT '','',0,value FROM setting WHERE key='session.archive_resurface'`, string(raw))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	archives, mode := map[Key]int64{}, "halt"
	for rows.Next() {
		var key Key
		var at int64
		var value string
		if err := rows.Scan(&key.Platform, &key.SessionID, &at, &value); err != nil {
			return nil, "", err
		}
		if key.Platform == "" && key.SessionID == "" {
			mode = value
		} else {
			archives[key] = at
		}
	}
	return archives, mode, rows.Err()
}
