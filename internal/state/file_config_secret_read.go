package state

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ocman/ocman/internal/sqliteutil"
)

// ReadConfigSecret resolves a startup reference without migrations or writable
// state access. The secret must already exist in this instance's database.
func ReadConfigSecret(ctx context.Context, path, id string) (string, error) {
	db, err := sqliteutil.OpenPool(fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000&_pragma=query_only(1)", path),
		"ocman", sqliteutil.Pool{MaxOpen: 1, MaxIdle: 1})
	if err != nil {
		return "", err
	}
	defer db.Close()
	d := &DB{db: db, dataDir: filepath.Dir(path)}
	return d.loadConfigSecret(ctx, id)
}
