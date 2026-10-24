package sessioncatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/state"
)

func New(db *state.DB) (*Store, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	return &Store{db: db.SQL()}, nil
}

const columns = `id,owner_id,platform,directory,title,COALESCE(parent_id,''),parent_native_id,purged,origin,origin_id,
	creation_state,creation_error,source_key,native_id,created_at,updated_at,legacy_keys`

type scanner interface{ Scan(...any) error }
type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scan(row scanner) (Session, error) {
	var s Session
	err := row.Scan(&s.ID, &s.OwnerID, &s.Platform, &s.Directory, &s.Title, &s.ParentID, &s.NativeParentID, &s.Purged,
		&s.Origin, &s.OriginID, &s.CreationState, &s.CreationError, &s.SourceKey, &s.NativeID, &s.CreatedAt, &s.UpdatedAt, &s.LegacyKeys)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func get(ctx context.Context, q queryer, id string) (Session, error) {
	return scan(q.QueryRowContext(ctx, `SELECT `+columns+` FROM session_catalog WHERE id=?`, id))
}

const bindingQuery = `SELECT ` + columns + ` FROM session_catalog
	WHERE owner_id=? AND platform=? AND source_key=? AND native_id=? AND native_id<>''`

func findBinding(ctx context.Context, q queryer, b Binding) (Session, error) {
	return scan(q.QueryRowContext(ctx, bindingQuery, b.OwnerID, b.Platform, b.SourceKey, b.NativeID))
}

func (s *Store) Get(ctx context.Context, id string) (Session, error) {
	return get(ctx, s.db, id)
}

func (s *Store) FindBinding(ctx context.Context, b Binding) (Session, error) {
	if !b.valid() {
		return Session{}, ErrInvalid
	}
	return findBinding(ctx, s.db, b)
}

func (s *Store) List(ctx context.Context, ownerID string, limit, offset int) ([]Session, error) {
	if !nonempty(ownerID) || limit < 1 || limit > 500 || offset < 0 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM session_catalog
		WHERE owner_id=? ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, ownerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func validParent(ctx context.Context, q queryer, owner, parent string) error {
	if parent == "" {
		return nil
	}
	row, err := get(ctx, q, parent)
	if err != nil {
		return err
	}
	if row.OwnerID != owner {
		return fmt.Errorf("parent belongs to another owner: %w", ErrInvalid)
	}
	return nil
}
