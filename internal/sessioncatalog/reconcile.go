package sessioncatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/startup"
)

// Reconcile upserts metadata without touching native execution or historical
// messages. Missing inventory entries never authorize deletion. A late parent
// is linked to already-imported children in the same transaction.
func (s *Store) Reconcile(ctx context.Context, source Source, sessions []NativeSession) (ReconcileResult, error) {
	var result ReconcileResult
	if !nonempty(source.OwnerID) || !nonempty(source.Platform) || !nonempty(source.SourceKey) {
		return result, ErrInvalid
	}
	seen := make(map[string]bool, len(sessions))
	for _, row := range sessions {
		if !nonempty(row.ID) || row.ID == row.ParentID || seen[row.ID] || !validDiscoveryOrigin(row.Origin, row.OriginID) {
			return result, ErrInvalid
		}
		seen[row.ID] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	rows := make([]Session, 0, len(sessions))
	startup.Report(ctx, "Reconciling session identities", 0, len(sessions))
	for i, metadata := range sessions {
		row, created, deferred, err := reconcileRow(ctx, tx, source, metadata, now)
		if err != nil {
			return ReconcileResult{}, err
		}
		if deferred {
			result.Deferred++
			startup.Report(ctx, "Reconciling session identities", i+1, len(sessions))
			continue
		}
		if created {
			result.Created++
		}
		rows = append(rows, row)
		startup.Report(ctx, "Reconciling session identities", i+1, len(sessions))
	}
	startup.Report(ctx, "Linking native session parents", 0, len(rows))
	for i, row := range rows {
		if err := linkNativeParent(ctx, tx, row, now); err != nil {
			return ReconcileResult{}, err
		}
		startup.Report(ctx, "Linking native session parents", i+1, len(rows))
	}
	startup.Report(ctx, "Validating session parent links", 0, len(rows))
	for i, row := range rows {
		if err := checkParentChain(ctx, tx, row.ID); err != nil {
			return ReconcileResult{}, err
		}
		startup.Report(ctx, "Validating session parent links", i+1, len(rows))
	}
	if err := tx.Commit(); err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

func checkParentChain(ctx context.Context, tx *sql.Tx, id string) error {
	seen := map[string]bool{}
	for id != "" {
		if seen[id] {
			return fmt.Errorf("session parent cycle: %w", ErrInvalid)
		}
		seen[id] = true
		row, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		id = row.ParentID
	}
	return nil
}

func validDiscoveryOrigin(origin Origin, id string) bool {
	switch origin {
	case "", External:
		return id == ""
	case Routine, Factory, Plugin:
		return nonempty(id)
	default:
		return false
	}
}

func reconcileRow(ctx context.Context, tx *sql.Tx, source Source, metadata NativeSession, now int64) (Session, bool, bool, error) {
	b := Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: metadata.ID}
	row, err := findBinding(ctx, tx, b)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return row, false, false, err
	}
	created := errors.Is(err, ErrNotFound)
	if created {
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_catalog
			WHERE owner_id=? AND platform=? AND creation_state='creating' AND (directory=? OR ?='')
			AND (?<=0 OR created_at<=?))`,
			source.OwnerID, source.Platform, metadata.Directory, metadata.Directory, metadata.CreatedAt, metadata.CreatedAt).Scan(&pending); err != nil {
			return row, false, false, err
		}
		if pending {
			return row, false, true, nil
		}
		origin := metadata.Origin
		if origin == "" {
			origin = External
		}
		row = Session{ID: "ocm_" + uuid.NewString(), OwnerID: source.OwnerID, Platform: source.Platform,
			SourceKey: source.SourceKey, NativeID: metadata.ID, Directory: metadata.Directory, Title: metadata.Title,
			Origin: origin, OriginID: metadata.OriginID, CreationState: Ready, CreatedAt: now, UpdatedAt: now}
		if err := insert(ctx, tx, row, "", ""); err != nil {
			return row, false, false, err
		}
	}
	// Unfinished creation owns its intended metadata until configuration is
	// complete. Inventory must not silently complete or undo a failed create.
	if row.CreationState != Ready || row.Purged {
		return row, created, !row.Purged && (row.CreationState == Creating || row.CreationState == Configuring), nil
	}
	origin, originID := row.Origin, row.OriginID
	if origin == External && metadata.Origin != "" && metadata.Origin != External {
		origin, originID = metadata.Origin, metadata.OriginID
	}
	if row.Title != metadata.Title || row.Directory != metadata.Directory || row.NativeParentID != metadata.ParentID || row.Origin != origin || row.OriginID != originID {
		// Clearing a native parent clears the derived parent link, but never
		// clears a parent explicitly reserved before native creation.
		if row.NativeParentID != "" && metadata.ParentID == "" {
			row.ParentID = ""
		}
		_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET title=?,directory=?,parent_native_id=?,parent_id=NULLIF(?,''),origin=?,origin_id=?,updated_at=? WHERE id=?`,
			metadata.Title, metadata.Directory, metadata.ParentID, row.ParentID, origin, originID, now, row.ID)
		if err != nil {
			return row, false, false, err
		}
		row.Title, row.Directory, row.NativeParentID, row.Origin, row.OriginID = metadata.Title, metadata.Directory, metadata.ParentID, origin, originID
	}
	return row, created, false, nil
}

func linkNativeParent(ctx context.Context, tx *sql.Tx, row Session, now int64) error {
	if row.CreationState != Ready || row.Purged {
		return nil
	}
	parent, err := nativeParent(ctx, tx, row)
	if err != nil {
		return err
	}
	if row.NativeParentID != "" && parent == "" {
		// A retained child may name a parent already deleted from the source.
		// Preserve that reference as a stable identity, but never steal a
		// parent whose native create is still awaiting its returned ID.
		placeholder, _, deferred, err := reconcileRow(ctx, tx, Source{OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: row.SourceKey}, NativeSession{ID: row.NativeParentID}, now)
		if err != nil {
			return err
		}
		if !deferred {
			parent = placeholder.ID
		}
	}
	if row.NativeParentID != "" && row.ParentID != parent {
		if _, err := tx.ExecContext(ctx, `UPDATE session_catalog SET parent_id=NULLIF(?,''),updated_at=? WHERE id=?`, parent, now, row.ID); err != nil {
			return err
		}
	}
	// Repair a child imported before this parent, without rescanning history.
	_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET parent_id=?,updated_at=?
		WHERE owner_id=? AND platform=? AND source_key=? AND parent_native_id=?
		AND creation_state='ready' AND COALESCE(parent_id,'')<>?`,
		row.ID, now, row.OwnerID, row.Platform, row.SourceKey, row.NativeID, row.ID)
	return err
}

func nativeParent(ctx context.Context, tx *sql.Tx, row Session) (string, error) {
	seen := map[string]bool{row.NativeID: true}
	parent, first := row.NativeParentID, ""
	for parent != "" {
		if seen[parent] {
			return "", fmt.Errorf("native session parent cycle: %w", ErrInvalid)
		}
		seen[parent] = true
		next, err := findBinding(ctx, tx, Binding{OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: row.SourceKey, NativeID: parent})
		if errors.Is(err, ErrNotFound) {
			return first, nil
		}
		if err != nil {
			return "", err
		}
		if first == "" {
			first = next.ID
		}
		parent = next.NativeParentID
	}
	return first, nil
}
