package claudeprovider

import (
	"context"
	"database/sql"
	"errors"
)

func (x *Index) scanForIndex(ctx context.Context, path string, saved *indexCursor) (*entry, *indexCursor, bool, error) {
	e, cursor, delta, err := scanIndex(path, x.projects, saved)
	if err != nil || !delta {
		return e, cursor, delta, err
	}
	// Native transcripts can replay an older message ID after another group.
	// Such a rewrite is not an append to the indexed message range. Rebuild
	// instead of counting its historical token contribution twice.
	for _, row := range e.usage {
		if row.ID == saved.LastID {
			continue
		}
		var exists int
		err := x.db.QueryRowContext(ctx, `SELECT 1 FROM usage WHERE session_id=? AND id=?`, e.ID, row.ID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, nil, false, err
		}
		return scanIndex(path, x.projects, nil)
	}
	return e, cursor, true, nil
}
