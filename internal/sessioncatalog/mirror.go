package sessioncatalog

import (
	"context"
	"errors"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

func IsID(id string) bool {
	return identity.IsSessionID(id)
}

// Mirror remembers a remote owner's existing identity. The caller must first
// authenticate its OwnerID against the live connection, not the response body.
// Mirroring never allocates another ID or rewrites an existing native binding.
func (s *Store) Mirror(ctx context.Context, row Session) error {
	if !IsID(row.ID) || !nonempty(row.OwnerID) || !nonempty(row.Platform) || !nonempty(row.SourceKey) || !nonempty(row.NativeID) || row.CreationState != Ready {
		return ErrInvalid
	}
	if row.ParentID == row.ID || row.NativeParentID == row.NativeID {
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
	previous, err := get(ctx, tx, row.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if exists && (previous.OwnerID != row.OwnerID || previous.Platform != row.Platform || previous.SourceKey != row.SourceKey || previous.NativeID != row.NativeID) {
		return ErrBindingConflict
	}
	b := Binding{OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: row.SourceKey, NativeID: row.NativeID}
	if other, err := findBinding(ctx, tx, b); err == nil && other.ID != row.ID {
		return ErrBindingConflict
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if exists && previous.UpdatedAt > row.UpdatedAt {
		return nil
	}
	if row.ParentID != "" {
		parent, err := get(ctx, tx, row.ParentID)
		if errors.Is(err, ErrNotFound) {
			row.ParentID = ""
		} else if err != nil {
			return err
		} else if parent.OwnerID != row.OwnerID {
			return ErrBindingConflict
		}
	}
	if !exists {
		if err := insert(ctx, tx, row, "", ""); err != nil {
			return err
		}
	}
	row.Purged = row.Purged || previous.Purged
	if row.Purged {
		row.Directory, row.Title, row.ParentID, row.NativeParentID = "", "", "", ""
	}
	_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET parent_id=NULLIF(?,''),parent_native_id=?,origin=?,origin_id=?,purged=?,
		title=CASE WHEN ? THEN '' ELSE title END,directory=CASE WHEN ? THEN '' ELSE directory END,updated_at=? WHERE id=?`,
		row.ParentID, row.NativeParentID, row.Origin, row.OriginID, row.Purged, row.Purged, row.Purged, row.UpdatedAt, row.ID)
	if err != nil {
		return err
	}
	if err := checkParentChain(ctx, tx, row.ID); err != nil {
		return err
	}
	return tx.Commit()
}
