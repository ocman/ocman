package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (d *DB) ProjectsCache(ctx context.Context) ([]byte, time.Time, error) {
	var data []byte
	var refreshedAt int64
	err := d.db.QueryRowContext(ctx, `SELECT projects_json, refreshed_at FROM projects_cache WHERE id = 1`).Scan(&data, &refreshedAt)
	if err == sql.ErrNoRows {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("reading projects cache: %w", err)
	}
	return data, time.UnixMilli(refreshedAt), nil
}

func (d *DB) SaveProjectsCache(ctx context.Context, data []byte, refreshedAt time.Time) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO projects_cache (id, projects_json, refreshed_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET projects_json = excluded.projects_json, refreshed_at = excluded.refreshed_at
	`, data, refreshedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("saving projects cache: %w", err)
	}
	return nil
}
