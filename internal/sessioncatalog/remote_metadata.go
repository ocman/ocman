package sessioncatalog

import (
	"context"
	"database/sql"
	"errors"
)

func migrateRemoteMetadata(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE session_remote_metadata (
		id TEXT PRIMARY KEY CHECK(substr(id,1,4)='ocm_' AND length(id)=40),
		owner_id TEXT NOT NULL CHECK(owner_id<>''),platform TEXT NOT NULL CHECK(platform<>''),source_key TEXT NOT NULL CHECK(source_key<>''),
		parent_id TEXT NOT NULL DEFAULT '',origin TEXT NOT NULL,origin_id TEXT NOT NULL DEFAULT '',
		purged INTEGER NOT NULL CHECK(purged IN (0,1)),created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
		CREATE INDEX session_remote_metadata_owner ON session_remote_metadata(owner_id,platform);`)
	return err
}

func remoteMetadata(ctx context.Context, q queryer, id string) (Session, error) {
	row := Session{CreationState: Ready}
	err := q.QueryRowContext(ctx, `SELECT id,owner_id,platform,source_key,parent_id,origin,origin_id,purged,created_at,updated_at FROM session_remote_metadata WHERE id=?`, id).
		Scan(&row.ID, &row.OwnerID, &row.Platform, &row.SourceKey, &row.ParentID, &row.Origin, &row.OriginID, &row.Purged, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

func canonicalMetadata(ctx context.Context, q queryer, id string) (Session, error) {
	row, err := remoteMetadata(ctx, q, id)
	if !errors.Is(err, ErrNotFound) {
		return row, err
	}
	return get(ctx, q, id)
}

// GetCanonical reads owner metadata without requiring a remote execution
// binding on this machine. Remote metadata never stores native session IDs.
func (s *Store) GetCanonical(ctx context.Context, id string) (Session, error) {
	return canonicalMetadata(ctx, s.db, id)
}

// MirrorCanonical accepts only an already allocated identity from an
// authenticated owner. Callers validate that owner against the live connection.
// It neither allocates identities nor installs a local execution binding.
func (s *Store) MirrorCanonical(ctx context.Context, row Session) error {
	if !IsID(row.ID) || !nonempty(row.OwnerID) || !nonempty(row.Platform) || !nonempty(row.SourceKey) || row.CreationState != Ready || row.NativeID != "" || row.NativeParentID != "" || row.ParentID == row.ID || (row.ParentID != "" && !IsID(row.ParentID)) {
		return ErrInvalid
	}
	if !validDiscoveryOrigin(row.Origin, row.OriginID) && (row.Origin != User || row.OriginID != "") {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	previous, err := canonicalMetadata(ctx, tx, row.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil {
		if previous.OwnerID != row.OwnerID || previous.Platform != row.Platform || previous.SourceKey != row.SourceKey {
			return ErrBindingConflict
		}
		if previous.UpdatedAt > row.UpdatedAt {
			return nil
		}
		row.Purged = row.Purged || previous.Purged
		row.CreatedAt = previous.CreatedAt
	}
	if row.Purged {
		row.ParentID = ""
	}
	var foreignChild bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_remote_metadata WHERE parent_id=? AND owner_id<>?)`, row.ID, row.OwnerID).Scan(&foreignChild); err != nil {
		return err
	}
	if foreignChild {
		return ErrBindingConflict
	}
	seen := map[string]bool{row.ID: true}
	for parent := row.ParentID; parent != ""; {
		if seen[parent] {
			return ErrInvalid
		}
		seen[parent] = true
		next, err := canonicalMetadata(ctx, tx, parent)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return err
		}
		if next.OwnerID != row.OwnerID {
			return ErrBindingConflict
		}
		parent = next.ParentID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_remote_metadata(id,owner_id,platform,source_key,parent_id,origin,origin_id,purged,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id,origin=excluded.origin,origin_id=excluded.origin_id,
		purged=excluded.purged,updated_at=excluded.updated_at`, row.ID, row.OwnerID, row.Platform, row.SourceKey, row.ParentID, row.Origin, row.OriginID, row.Purged, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}
