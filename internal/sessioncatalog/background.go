package sessioncatalog

import (
	"context"
	"database/sql"
)

func migrateBackgroundLifecycle(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_background_lifecycle (
	 session_id TEXT NOT NULL REFERENCES session_catalog(id) ON DELETE CASCADE,
	 task_id TEXT NOT NULL CHECK(task_id<>''), lifecycle TEXT NOT NULL CHECK(lifecycle IN ('task','service')),
	 PRIMARY KEY(session_id,task_id)) WITHOUT ROWID`)
	return err
}

func (s *Store) SetBackgroundLifecycle(ctx context.Context, sessionID, taskID, lifecycle string) error {
	if taskID == "" || (lifecycle != "task" && lifecycle != "service") {
		return ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO session_background_lifecycle VALUES(?,?,?)
	 ON CONFLICT(session_id,task_id) DO UPDATE SET lifecycle=excluded.lifecycle`, sessionID, taskID, lifecycle)
	return err
}

// BackgroundLifecycles returns native-keyed overrides for exactly one source.
func (s *Store) BackgroundLifecycles(ctx context.Context, owner, platform, source string) (map[string]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.native_id,b.task_id,b.lifecycle FROM session_background_lifecycle b
	 JOIN session_catalog c ON c.id=b.session_id WHERE c.owner_id=? AND c.platform=? AND c.source_key=? AND c.purged=0`, owner, platform, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var id, task, lifecycle string
		if err := rows.Scan(&id, &task, &lifecycle); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		out[id][task] = lifecycle
	}
	return out, rows.Err()
}
