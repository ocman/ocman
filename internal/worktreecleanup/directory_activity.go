package worktreecleanup

import (
	"context"
	"path/filepath"
)

// A directory watermark covers every session in the checkout, including hidden
// children and sessions not yet ingested. Admission never needs their inventory.
func (m *Module) touchDirectory(ctx context.Context, owner, path string) error {
	_, err := m.d.Store.SQL().ExecContext(ctx, `INSERT INTO worktree_cleanup_directory_activity
		(owner,path,activity_at) VALUES(?,?,?) ON CONFLICT(owner,path)
		DO UPDATE SET activity_at=MAX(activity_at,excluded.activity_at)`, owner, filepath.Clean(path), m.now().UnixMilli())
	return err
}

func (m *Module) directoryActivity(ctx context.Context, owner, path string) (int64, error) {
	path = filepath.Clean(path)
	var at int64
	err := m.d.Store.SQL().QueryRowContext(ctx, `SELECT COALESCE(MAX(activity_at),0)
		FROM worktree_cleanup_directory_activity WHERE owner=?
		AND (path=? OR substr(?,1,length(path)+1)=path||?)`, owner, path, path, string(filepath.Separator)).Scan(&at)
	return at, err
}
