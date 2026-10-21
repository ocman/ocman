package sessioncatalog

import "context"

const legacyBindingQuery = `SELECT ` + columns + ` FROM session_catalog WHERE owner_id=? AND platform=? AND native_id=? LIMIT 2`

// FindLegacyBinding resolves a source-less legacy association only when this
// owner's complete catalogue has exactly one physical binding. Selecting the
// currently active source would misattribute state after a source replacement.
func (s *Store) FindLegacyBinding(ctx context.Context, owner, platform, native string) (Session, error) {
	if !nonempty(owner) || !nonempty(platform) || !nonempty(native) || IsID(native) {
		return Session{}, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, legacyBindingQuery, owner, platform, native)
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNotFound
	}
	row, err := scan(rows)
	if err != nil {
		return Session{}, err
	}
	if rows.Next() {
		return Session{}, ErrBindingConflict
	}
	return row, rows.Err()
}
