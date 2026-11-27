package pluginsmod

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func (m *Module) pluginRunParent(ctx context.Context, pluginID, platformID, directory, parentRun string) (string, error) {
	if parentRun == "" {
		return "", nil
	}
	denied := &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	var parentID string
	err := m.plugins.db.QueryRowContext(ctx, `SELECT r.session_id FROM plugin_session_run r
		JOIN plugin_session s ON s.session_id=r.session_id AND s.plugin_id=r.plugin_id
		WHERE r.plugin_id=? AND r.operation_id=? AND r.settled=1 AND r.error_category='' AND s.platform_id=?`, pluginID, parentRun, platformID).Scan(&parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", denied
	}
	if err != nil || m.deps.Adapter == nil {
		return "", plugins.ErrUnavailable
	}
	adapter, ok := m.deps.Adapter(ctx, platformID, parentID)
	if !ok {
		return "", plugins.ErrUnavailable
	}
	parent, err := platforms.ReadSessionMetadata(ctx, adapter, parentID)
	if err != nil {
		return "", plugins.ErrUnavailable
	}
	if parent == nil || parent.ParentID != "" {
		return "", denied
	}
	parentDir, err := filepath.EvalSymlinks(parent.Directory)
	if err != nil || parentDir != directory {
		return "", denied
	}
	return parentID, nil
}
