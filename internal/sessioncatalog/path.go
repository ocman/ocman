package sessioncatalog

import "context"

// FilePath is used only to attach the authoritative catalogue read-only.
func (s *Store) FilePath(ctx context.Context) (string, error) {
	var sequence int
	var name, path string
	err := s.db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&sequence, &name, &path)
	return path, err
}
