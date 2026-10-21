package statev2

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const projectsCacheSchema = `CREATE TABLE projects_cache (
	owner_id TEXT PRIMARY KEY CHECK(owner_id <> ''),
	projects_json BLOB NOT NULL,
	refreshed_at INTEGER NOT NULL
);`

// ProjectsCache is disposable alongside the conversations it summarizes. Never
// recover it from state.db, where it could survive an explicit history reset.
func (d *DB) ProjectsCache(ctx context.Context, owner string) ([]byte, time.Time, error) {
	var data []byte
	var at int64
	err := d.reader().QueryRowContext(ctx, `SELECT projects_json,refreshed_at FROM projects_cache WHERE owner_id=?`, owner).Scan(&data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, time.UnixMilli(at), nil
}

func (d *DB) SaveProjectsCache(ctx context.Context, owner string, data []byte, at time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO projects_cache(owner_id,projects_json,refreshed_at) VALUES(?,?,?)
		ON CONFLICT(owner_id) DO UPDATE SET projects_json=excluded.projects_json,refreshed_at=excluded.refreshed_at`, owner, data, at.UnixMilli())
	return err
}
