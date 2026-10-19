package statev2

import (
	"context"
	"errors"
)

// SessionState is a bounded metadata read for ingestion, including purge state.
// A missing unpurged session has revision zero. A later Commit still checks the
// expected revision and purge tombstone atomically, closing concurrent-write races.
func (d *DB) SessionState(ctx context.Context, key Key) (Session, bool, error) {
	s := Session{Key: key}
	var purged bool
	if err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM purged_session WHERE instance_id=? AND session_id=?)`,
		key.InstanceID, key.SessionID).Scan(&purged); err != nil {
		return s, false, err
	}
	if purged {
		return s, true, nil
	}
	var raw string
	err := d.db.QueryRowContext(ctx, `SELECT title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent
		FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID).
		Scan(&s.Title, &s.Directory, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &raw, &s.ParentID, &s.ProjectID, &s.ReadFormat, &s.Subagent)
	if errors.Is(notFound(err), ErrNotFound) {
		return s, false, nil
	}
	s.Extensions = []byte(raw)
	return s, false, err
}

// Sessions inventories retained metadata without loading conversation content.
// Purged identities are deliberately absent, source-deleted sessions are retained.
func (d *DB) Sessions(ctx context.Context, instanceID string) ([]Session, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent
		FROM session WHERE instance_id=? ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		s := Session{Key: Key{InstanceID: instanceID}}
		var raw string
		if err := rows.Scan(&s.SessionID, &s.Title, &s.Directory, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &raw, &s.ParentID, &s.ProjectID, &s.ReadFormat, &s.Subagent); err != nil {
			return nil, err
		}
		s.Extensions = []byte(raw)
		out = append(out, s)
	}
	return out, rows.Err()
}
