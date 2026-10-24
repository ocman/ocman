package statev2

import (
	"context"
	"errors"
)

// SessionState is a bounded metadata read for ingestion. A missing session has
// revision zero. A later Commit still checks the expected revision atomically,
// closing concurrent-write races.
func (d *DB) SessionState(ctx context.Context, key Key) (Session, error) {
	s := Session{Key: key}
	var raw string
	err := d.reader().QueryRowContext(ctx, `SELECT title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent
		FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID).
		Scan(&s.Title, &s.Directory, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &raw, &s.ParentID, &s.ProjectID, &s.ReadFormat, &s.Subagent)
	if errors.Is(notFound(err), ErrNotFound) {
		return s, nil
	}
	s.Extensions = []byte(raw)
	return s, err
}

// Sessions inventories retained metadata without loading conversation content.
// Source-deleted sessions are retained.
func (d *DB) Sessions(ctx context.Context, instanceID string) ([]Session, error) {
	rows, err := d.reader().QueryContext(ctx, `SELECT id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent
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
