package sidebar

import (
	"context"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// Session reads one eligible compact row, using requested only as an identity
// filter, not as permission to surface deleted or otherwise hidden history.
func (r *Reader) Session(ctx context.Context, scope Scope, id string) (*db.Session, error) {
	rows, err := r.sessionsFor(ctx, []Scope{scope}, platforms.SidebarQuery{IncludeIDs: []string{id}}, singleSessionQuery())
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, platforms.ErrNotFound
	}
	return &rows[0], nil
}

// Requested is only an identity filter here, never permission to surface
// deleted or otherwise hidden history.
func singleSessionQuery() string {
	return strings.ReplaceAll(selectByIdentity("requested", ""), " OR s.id IN requested", "")
}
