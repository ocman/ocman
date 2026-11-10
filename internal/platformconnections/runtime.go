package platformconnections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/state"
)

func migrateConnectionRuntimes(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_connection_runtime (
		connection_id TEXT NOT NULL REFERENCES platform_connection(id),
		platform TEXT NOT NULL, repo_root TEXT NOT NULL,
		endpoint TEXT NOT NULL, kind TEXT NOT NULL, runtime_id TEXT NOT NULL,
		pid INTEGER NOT NULL, launched_at INTEGER NOT NULL,
		PRIMARY KEY(connection_id, platform, repo_root))`)
	return err
}

// RuntimeStore scopes supervisor handles without changing native identities.
type RuntimeStore struct {
	db       *sql.DB
	id, kind string
}

func NewRuntimeStore(ctx context.Context, db *state.DB, connection Connection) (*RuntimeStore, error) {
	if db == nil {
		return nil, fmt.Errorf("connection runtime requires owner state")
	}
	saved, err := New(db).Get(ctx, connection.ID)
	if err != nil {
		return nil, err
	}
	if saved.Kind != connection.Kind || saved.Directory != connection.Directory {
		return nil, fmt.Errorf("%w: runtime connection binding changed", ErrInvalid)
	}
	if !saved.Enabled || saved.Removed {
		return nil, ErrUnavailable
	}
	return &RuntimeStore{db: db.SQL(), id: saved.ID, kind: saved.Kind}, nil
}

func (s *RuntimeStore) check(platform string) error {
	if platform != s.kind {
		return fmt.Errorf("%w: runtime platform differs from connection", ErrInvalid)
	}
	return nil
}

func (s *RuntimeStore) GetManagedProvider(ctx context.Context, platform, root string) (state.ManagedInstance, bool, error) {
	var inst state.ManagedInstance
	if err := s.check(platform); err != nil {
		return inst, false, err
	}
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT endpoint,kind,runtime_id,pid,launched_at FROM platform_connection_runtime WHERE connection_id=? AND platform=? AND repo_root=?`, s.id, platform, root).
		Scan(&inst.Endpoint, &inst.Kind, &inst.RuntimeID, &inst.PID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return inst, false, nil
	}
	inst.LaunchedAt = time.Unix(at, 0)
	return inst, err == nil, err
}

func (s *RuntimeStore) UpsertManagedProvider(ctx context.Context, platform, root string, inst state.ManagedInstance) error {
	if err := s.check(platform); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO platform_connection_runtime(connection_id,platform,repo_root,endpoint,kind,runtime_id,pid,launched_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,platform,repo_root) DO UPDATE SET endpoint=excluded.endpoint,kind=excluded.kind,runtime_id=excluded.runtime_id,pid=excluded.pid,launched_at=excluded.launched_at`,
		s.id, platform, root, inst.Endpoint, inst.Kind, inst.RuntimeID, inst.PID, inst.LaunchedAt.Unix())
	return err
}

func (s *RuntimeStore) DeleteManagedProvider(ctx context.Context, platform, root string) error {
	if err := s.check(platform); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM platform_connection_runtime WHERE connection_id=? AND platform=? AND repo_root=?`, s.id, platform, root)
	return err
}

func (s *RuntimeStore) ListManagedProviders(ctx context.Context, platform string) ([]string, error) {
	if err := s.check(platform); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repo_root FROM platform_connection_runtime WHERE connection_id=? AND platform=? ORDER BY repo_root`, s.id, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}
