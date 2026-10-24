package statev2

import (
	"context"
	"fmt"
)

// SessionMetadata contains identity and display metadata only. Indexing must
// not read conversation content or opaque source extensions.
type SessionMetadata struct {
	Instance Instance
	Session  Session
}

// MetadataPage inventories retained sessions on one owner. A cursor is the
// final row's Key.
func (d *DB) MetadataPage(ctx context.Context, owner string, after Key, limit int) ([]SessionMetadata, error) {
	if owner == "" || limit < 1 || limit > 500 {
		return nil, fmt.Errorf("statev2: invalid metadata page")
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT i.id,i.owner_id,i.platform,i.source_key,
		s.id,s.title,s.directory,s.parent_id,s.source_deleted,s.created_at,s.updated_at FROM session s
		JOIN instance i ON i.id=s.instance_id WHERE i.owner_id=? AND (s.instance_id,s.id)>(?,?)
		ORDER BY s.instance_id,s.id LIMIT ?`, owner, after.InstanceID, after.SessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionMetadata{}
	for rows.Next() {
		var m SessionMetadata
		if err := rows.Scan(&m.Instance.ID, &m.Instance.OwnerID, &m.Instance.Platform, &m.Instance.SourceKey,
			&m.Session.SessionID, &m.Session.Title, &m.Session.Directory, &m.Session.ParentID, &m.Session.SourceDeleted, &m.Session.CreatedAt, &m.Session.UpdatedAt); err != nil {
			return nil, err
		}
		m.Session.InstanceID = m.Instance.ID
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) Metadata(ctx context.Context, key Key) (SessionMetadata, error) {
	var m SessionMetadata
	err := d.reader().QueryRowContext(ctx, `SELECT i.id,i.owner_id,i.platform,i.source_key,
		s.id,s.title,s.directory,s.parent_id,s.source_deleted,s.created_at,s.updated_at FROM session s
		JOIN instance i ON i.id=s.instance_id WHERE s.instance_id=? AND s.id=?`, key.InstanceID, key.SessionID).
		Scan(&m.Instance.ID, &m.Instance.OwnerID, &m.Instance.Platform, &m.Instance.SourceKey,
			&m.Session.SessionID, &m.Session.Title, &m.Session.Directory, &m.Session.ParentID, &m.Session.SourceDeleted, &m.Session.CreatedAt, &m.Session.UpdatedAt)
	m.Session.InstanceID = m.Instance.ID
	return m, notFound(err)
}
