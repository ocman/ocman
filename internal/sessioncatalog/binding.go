package sessioncatalog

import (
	"context"
	"errors"
	"time"
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
