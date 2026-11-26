package remote

import (
	"path/filepath"
	"slices"

	"github.com/ocman/ocman/internal/forge"
)

// CachedForgeRemotes looks up the exact checkout, then the caller's folded
// worktree root. It never borrows a repository from another owner.
func (m *Manager) CachedForgeRemotes(owner, dir, root string) ([]forge.Remote, bool) {
	m.invMu.RLock()
	projects := m.inventory[owner]
	m.invMu.RUnlock()
	for _, path := range []string{dir, root} {
		for _, p := range projects {
			if filepath.Clean(p.Dir) == path && p.AssociatedDirectory == "" {
				return slices.Clone(p.ForgeRemotes), true
			}
		}
	}
	return nil, false
}
