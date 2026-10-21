package claudecode

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/db"
	gitpkg "github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

// A shared transcript listing can seed Knows on every supervisor. For owned
// sessions, the persisted project root is authoritative, even before that
// project's headless watcher sees its first event. An unattached owner must
// not fall through to a different project's cached transcript.
func (c *claudeProjects) owner(id string) *provider.Adapter {
	root, owned := claudeprovider.OwnedProjectRoot(c.statePath, id)
	if owned && root == "" {
		dir, _ := claudeprovider.OwnedDirectory(c.statePath, id)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		root, _ = gitpkg.ResolveMainRepoRoot(ctx, dir)
		if root == "" {
			root = dir
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, _ := c.boundOwnerLocked(id); a != nil {
		return a
	}
	if owned {
		if p := c.roots[root]; root != "" && p != nil {
			return p.adapter
		}
		return nil
	}
	var owner *provider.Adapter
	for _, p := range c.roots {
		live := p.adapter.LiveStatus(id)
		if live != nil && platforms.ReloadSessionActive(db.Session{Status: db.SessionStatus(live.Status), PendingPermission: live.PendingPermission, PendingQuestion: live.PendingQuestion}) {
			if owner != nil && owner != p.adapter {
				return nil
			}
			owner = p.adapter
		}
	}
	return owner
}
