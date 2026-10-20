package sessioncatalog

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Bind makes a previously reserved identity ready. Binding never changes its
// ID, owner or origin and cannot steal an already-discovered native identity.
func (s *Store) Bind(ctx context.Context, id string, b Binding) (Session, error) {
	return s.bindState(ctx, id, b, Ready)
}

// Attach persists the returned native identity before permission installation,
// origin linkage or publication. A crash during configuration can then be
// reconciled without losing the native execution ID or allocating a second ID.
func (s *Store) Attach(ctx context.Context, id string, b Binding) (Session, error) {
	return s.bindState(ctx, id, b, Configuring)
}

func (s *Store) bindState(ctx context.Context, id string, b Binding, target CreationState) (Session, error) {
	if !b.valid() {
		return Session{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := get(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if row.OwnerID != b.OwnerID || row.Platform != b.Platform {
		return Session{}, ErrInvalid
	}
	if row.CreationState == Ready || row.CreationState == Configuring {
		if row.SourceKey != b.SourceKey || row.NativeID != b.NativeID {
			return Session{}, ErrTransition
		}
		if row.CreationState == Ready || target == Configuring {
			return row, nil
		}
	}
	if row.CreationState != Creating && row.CreationState != Configuring {
		return Session{}, ErrTransition
	}
	if bound, err := findBinding(ctx, tx, b); err == nil && bound.ID != row.ID {
		return Session{}, ErrBindingConflict
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return Session{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET creation_state=?,source_key=?,native_id=?,updated_at=? WHERE id=?`, target, b.SourceKey, b.NativeID, time.Now().UnixMilli(), id)
	if err != nil {
		return Session{}, err
	}
	row, err = get(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	return row, tx.Commit()
}

// Fail records a definite failure, not an authorization to retry creation.
// Known native bindings remain reserved after confirmed disposal. An uncertain
// outcome must remain Creating/Configuring for explicit reconciliation.
func (s *Store) Fail(ctx context.Context, id, cause string) (Session, error) {
	if !nonempty(cause) {
		return Session{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := get(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	if row.CreationState == CreationFailed && row.CreationError == cause {
		return row, nil
	}
	if row.CreationState != Creating && row.CreationState != Configuring {
		return Session{}, ErrTransition
	}
	_, err = tx.ExecContext(ctx, `UPDATE session_catalog SET creation_state='creation-failed',creation_error=?,updated_at=? WHERE id=?`, cause, time.Now().UnixMilli(), id)
	if err != nil {
		return Session{}, err
	}
	row, err = get(ctx, tx, id)
	if err != nil {
		return Session{}, err
	}
	return row, tx.Commit()
}

type Discovery struct {
	Binding
	Directory string
	Title     string
	ParentID  string
}

// Discover gives external sessions the same identities as owner-created ones.
// Unknown native rows in a scope with an unfinished create are deferred: the
// platform may announce a session before returning the ID needed by Bind.
func (s *Store) Discover(ctx context.Context, d Discovery) (Session, bool, error) {
	if !d.valid() || !nonempty(d.Directory) {
		return Session{}, false, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if row, err := findBinding(ctx, tx, d.Binding); err == nil {
		return row, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Session{}, false, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM session_catalog WHERE owner_id=? AND platform=? AND directory=? AND creation_state='creating'`, d.OwnerID, d.Platform, d.Directory).Scan(&pending); err != nil {
		return Session{}, false, err
	}
	if pending > 0 {
		return Session{}, false, ErrCreating
	}
	if err := validParent(ctx, tx, d.OwnerID, d.ParentID); err != nil {
		return Session{}, false, err
	}
	now := time.Now().UnixMilli()
	row := Session{ID: "ocm_" + uuid.NewString(), OwnerID: d.OwnerID, Platform: d.Platform, Directory: d.Directory,
		Title: d.Title, ParentID: d.ParentID, Origin: External, CreationState: Ready,
		SourceKey: d.SourceKey, NativeID: d.NativeID, CreatedAt: now, UpdatedAt: now}
	if err := insert(ctx, tx, row, "", ""); err != nil {
		return Session{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, false, err
	}
	return row, true, nil
}
