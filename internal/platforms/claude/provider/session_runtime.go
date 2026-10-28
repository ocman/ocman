package claudeprovider

import (
	"fmt"
	"path/filepath"

	"github.com/ocman/ocman/internal/platforms"
)

// RebindSessionRuntime records a supervisor verified to be executing this native
// session. The working directory, title, config and resume state are unchanged.
// Use the shared ownership lock so another supervisor's updates are preserved.
func RebindSessionRuntime(statePath, id, root string) error {
	if id == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("invalid Claude session runtime binding")
	}
	o, found := readOwnedSession(statePath, id)
	if !found {
		return platforms.ErrNotFound
	}
	if o.ProjectRoot == root {
		return nil
	}
	l := &live{opts: Options{StatePath: statePath}}
	err := l.updateOwnedLocked(func() {
		o := l.owned[id]
		found = o != nil
		if found {
			o.ProjectRoot = root
		}
	})
	if err != nil {
		return err
	}
	if !found {
		return platforms.ErrNotFound
	}
	return nil
}
