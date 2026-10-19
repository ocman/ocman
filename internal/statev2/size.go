package statev2

import (
	"context"
	"fmt"
)

// SizeBytes reports SQLite's logical size, including pages committed in WAL,
// matching the existing state/OpenCode database-size analytics.
func (d *DB) SizeBytes(ctx context.Context) (int64, error) {
	var pages, size int64
	if err := d.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, fmt.Errorf("reading history page count: %w", err)
	}
	if err := d.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return 0, fmt.Errorf("reading history page size: %w", err)
	}
	return pages * size, nil
}
