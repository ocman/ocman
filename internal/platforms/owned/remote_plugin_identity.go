package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// RemotePluginReference supplies only an opaque legacy wire/deduplication
// correlation from an immutable owner proof. It never supplies execution IDs.
// A newly allocated session with no legacy association keeps its canonical key.
func (s *ReferenceStore) RemotePluginReference(ctx context.Context, scope, id string) (string, error) {
	owner, _ := platforms.SplitPlatformID(scope)
	if owner == "" || !sessioncatalog.IsID(id) {
		return "", sessioncatalog.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT native_id FROM platform_session_reference WHERE scope=? AND canonical_id=? LIMIT 2`, scope, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return id, nil
	}
	var legacy string
	if err := rows.Scan(&legacy); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", sessioncatalog.ErrBindingConflict
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return legacy, nil
}
