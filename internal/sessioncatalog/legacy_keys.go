package sessioncatalog

import "context"

// RetainLegacyKeys is called only by cutover migrations, before serving. It
// preserves opaque deduplication tokens for sessions with pre-cutover state.
// New identities use canonical tokens and never inherit native-ID collisions.
func (s *Store) RetainLegacyKeys(ctx context.Context, owner, id string) error {
	if owner == "" || !IsID(id) {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE session_catalog SET legacy_keys=1 WHERE id=? AND owner_id=?`, id, owner)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}
