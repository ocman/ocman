package pluginsmod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func (m *Module) pluginModelWorkspace(ctx context.Context, pluginID, workspaceID string) (string, error) {
	root, err := m.store.PluginDataDir(ctx, pluginID)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(workspaceID))
	for _, name := range []string{"model-workspaces", hex.EncodeToString(hash[:])} {
		root = filepath.Join(root, name)
		if err := os.Mkdir(root, 0o700); err != nil && !os.IsExist(err) {
			return "", plugins.ErrUnavailable
		}
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return "", plugins.ErrUnavailable
		}
	}
	return root, nil
}
