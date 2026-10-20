package sessioncatalog

import (
	"context"
	"time"
)

// Scrub retains an identity tombstone but removes display metadata after an
// explicit history purge. Later/stale inventories must never restore it.
func (s *Store) Scrub(ctx context.Context, source Source, nativeID string) error {
	if !nonempty(source.OwnerID) || !nonempty(source.Platform) || !nonempty(source.SourceKey) || !nonempty(nativeID) {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	row, _, deferred, err := reconcileRow(ctx, tx, source, NativeSession{ID: nativeID}, now)
	if err != nil {
		return err
	}
	// An attached unfinished binding can be scrubbed safely. Only an unknown
	// native ID must wait, to avoid splitting an in-flight creation's identity.
	if deferred && row.ID == "" {
		return ErrCreating
	}
	_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET purged=1,title='',directory='',parent_id=NULL,parent_native_id='',
		creation_error=CASE WHEN creation_state='creation-failed' THEN 'history purged' ELSE creation_error END,updated_at=? WHERE id=?`, now, row.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
