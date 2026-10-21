package state

import "context"

// SidebarFlags is the hub's display state for remote rows. One query reads
// only preference tables, never conversations, summaries or platform state.
type SidebarFlags struct {
	Seen                      map[Key]SeenSessionState
	Pinned, Archived, Snoozed map[Key]int64
	Projects                  map[ProjectKey]int64
	Mode                      string
}

func (d *DB) SidebarFlags(ctx context.Context) (SidebarFlags, error) {
	f := SidebarFlags{Seen: map[Key]SeenSessionState{}, Pinned: map[Key]int64{}, Archived: map[Key]int64{}, Snoozed: map[Key]int64{}, Projects: map[ProjectKey]int64{}, Mode: "halt"}
	rows, err := d.db.QueryContext(ctx, `SELECT 'seen',platform,session_id,session_time_updated,interrupted,'' FROM seen_session
	 UNION ALL SELECT 'pin',platform,session_id,pinned_at,0,'' FROM pinned_session
	 UNION ALL SELECT 'archive',platform,session_id,session_time_updated,snoozed_until,'' FROM archived_session
	 UNION ALL SELECT 'project',remote_id,project_root,archived_at,0,'' FROM archived_project
	 UNION ALL SELECT 'mode','','',0,0,value FROM setting WHERE key='session.archive_resurface'`)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, platform, id, value string
		var a, b int64
		if err := rows.Scan(&kind, &platform, &id, &a, &b, &value); err != nil {
			return f, err
		}
		key := Key{Platform: platform, SessionID: id}
		switch kind {
		case "seen":
			f.Seen[key] = SeenSessionState{TimeUpdated: a, Interrupted: b != 0}
		case "pin":
			f.Pinned[key] = a
		case "archive":
			f.Archived[key] = a
			f.Snoozed[key] = b
		case "project":
			f.Projects[ProjectKey{RemoteID: platform, Root: id}] = a
		case "mode":
			f.Mode = value
		}
	}
	return f, rows.Err()
}
