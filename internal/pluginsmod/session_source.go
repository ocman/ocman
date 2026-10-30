package pluginsmod

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

const maxSessionSourceFiles = 10000

// Pin exact prepared files rather than a wildcard read rule. This also denies
// raw paths containing ../ on adapters whose native tools preserve those paths.
// Plugins provide an inventory file for source discovery; search, shell, network,
// delegation and mutation tools deliberately remain denied.
func sourceSessionRules(ctx context.Context, directory string) ([]platforms.PermissionRule, error) {
	rules := []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "deny"}}
	files := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return plugins.ErrInvalidMessage
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || strings.ContainsAny(path, "\r\n\x00") {
			return plugins.ErrInvalidMessage
		}
		files++
		if files > maxSessionSourceFiles {
			return plugins.ErrInvalidMessage
		}
		rules = append(rules, platforms.PermissionRule{Permission: "read", Pattern: providerprotocol.ExactPattern(filepath.ToSlash(path)), Action: "allow"})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if files == 0 {
		return nil, plugins.ErrInvalidMessage
	}
	return rules, nil
}
