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
	Purged   bool
}

const metadataInventory = `WITH metadata AS (
	SELECT instance_id,id,title,directory,parent_id,source_deleted,created_at,updated_at,0 AS purged FROM session
	UNION ALL SELECT instance_id,session_id,'','','',0,0,0,1 FROM purged_session
) `

// MetadataPage inventories retained sessions and purge tombstones on one owner. A cursor is the
// final row's Key; concurrent inserts/updates are recovered through Changes.
func (d *DB) MetadataPage(ctx context.Context, owner string, after Key, limit int) ([]SessionMetadata, error) {
	if owner == "" || limit < 1 || limit > 500 {
		return nil, fmt.Errorf("statev2: invalid metadata page")
	}
	rows, err := d.db.QueryContext(ctx, metadataInventory+`SELECT i.id,i.owner_id,i.platform,i.source_key,
		s.id,s.title,s.directory,s.parent_id,s.source_deleted,s.created_at,s.updated_at,s.purged FROM metadata s
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
			&m.Session.SessionID, &m.Session.Title, &m.Session.Directory, &m.Session.ParentID, &m.Session.SourceDeleted, &m.Session.CreatedAt, &m.Session.UpdatedAt, &m.Purged); err != nil {
			return nil, err
		}
		m.Session.InstanceID = m.Instance.ID
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) Metadata(ctx context.Context, key Key) (SessionMetadata, error) {
	var m SessionMetadata
	err := d.db.QueryRowContext(ctx, metadataInventory+`SELECT i.id,i.owner_id,i.platform,i.source_key,
		s.id,s.title,s.directory,s.parent_id,s.source_deleted,s.created_at,s.updated_at,s.purged FROM metadata s
		JOIN instance i ON i.id=s.instance_id WHERE s.instance_id=? AND s.id=?`, key.InstanceID, key.SessionID).
		Scan(&m.Instance.ID, &m.Instance.OwnerID, &m.Instance.Platform, &m.Instance.SourceKey,
			&m.Session.SessionID, &m.Session.Title, &m.Session.Directory, &m.Session.ParentID, &m.Session.SourceDeleted, &m.Session.CreatedAt, &m.Session.UpdatedAt, &m.Purged)
	m.Session.InstanceID = m.Instance.ID
	return m, notFound(err)
}

// ChangeHead fences a metadata inventory. Consumers read Changes after this
// sequence once the inventory completes, so concurrent writes cannot be lost.
func (d *DB) ChangeHead(ctx context.Context) (int64, error) {
	var head int64
	err := d.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM change_outbox`).Scan(&head)
	return head, err
}
