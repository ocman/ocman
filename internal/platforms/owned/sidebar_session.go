package owned

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

func (a *Adapter) SidebarSession(ctx context.Context, id string) (*db.Session, error) {
	r, err := a.openSidebar(ctx)
	if err != nil {
		return nil, err
	}
	return r.Session(ctx, a.sidebarScope(), id)
}
