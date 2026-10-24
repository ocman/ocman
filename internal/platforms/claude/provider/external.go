package claudeprovider

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// resumable is intentionally independent of process ownership. An external
// instance is a warning, not a reason to refuse a user's next message.
func (l *live) resumable(s *db.Session) bool {
	return s.ParentID == "" && filepath.IsAbs(s.Directory) && l.versionErr == nil
}

// claimForTurn adopts an existing transcript only when the user sends. Reads
// never create ownership or start a runtime. The first child must use --resume.
func (l *live) claimForTurn(ctx context.Context, id string) (owned, error) {
	if o, ok := l.ownedSession(id); ok {
		return o, nil
	}
	e, err := l.idx.get(ctx, id)
	if err != nil {
		return owned{}, err
	}
	if e == nil {
		return owned{}, platforms.ErrNotFound
	}
	if e.ParentID != "" || e.RootID != e.ID || !filepath.IsAbs(e.Directory) {
		return owned{}, statusError{http.StatusForbidden, errReadOnly}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err = l.updateOwnedLocked(func() {
		if l.owned[id] == nil {
			l.owned[id] = &owned{Directory: e.Directory, ProjectRoot: l.opts.ProjectRoot,
				Created: e.Created, Started: true, Config: l.opts.ConfigDir}
		}
	})
	if err != nil {
		return owned{}, err
	}
	return *l.owned[id], nil
}
