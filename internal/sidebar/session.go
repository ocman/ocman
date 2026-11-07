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
	query := strings.Replace(selectSessions, "WHERE i.owner_id=?", "WHERE s.id IN requested AND i.owner_id=?", 1)
	query = strings.ReplaceAll(query, " OR s.id IN requested", "")
	rows, err := r.sessionsFor(ctx, []Scope{scope}, platforms.SidebarQuery{IncludeIDs: []string{id}}, query)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, platforms.ErrNotFound
	}
	return &rows[0], nil
}
