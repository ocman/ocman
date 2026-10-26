package db

import (
	"context"
	"fmt"
)

// SizeBytes returns the logical size, including pages committed in WAL.
func (d *DB) SizeBytes(ctx context.Context) (int64, error) {
	var pages, size int64
	if err := d.reads().QueryRowContext(ctx, `PRAGMA main.page_count`).Scan(&pages); err != nil {
		return 0, fmt.Errorf("reading OpenCode page count: %w", err)
	}
	if err := d.reads().QueryRowContext(ctx, `PRAGMA main.page_size`).Scan(&size); err != nil {
		return 0, fmt.Errorf("reading OpenCode page size: %w", err)
	}
	return pages * size, nil
}
