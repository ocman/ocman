package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sidebar"
)

func (a *Adapter) RunningSessionCount(ctx context.Context, q platforms.SidebarQuery) (int, error) {
	source, err := a.SidebarSource(ctx)
	if err != nil {
		return 0, err
	}
	return a.RunningSessionCountFor(ctx, []platforms.SidebarSource{source}, q)
}

func (a *Adapter) RunningSessionCountFor(ctx context.Context, sources []platforms.SidebarSource, q platforms.SidebarQuery) (int, error) {
	r, err := a.openSidebar(ctx)
	if err != nil {
		return 0, err
	}
	scopes := make([]sidebar.Scope, 0, len(sources))
	for _, s := range sources {
		if s.Owner != a.owner {
			return 0, platforms.ErrHistoryUnavailable
		}
		scopes = append(scopes, sidebar.Scope{Owner: s.Owner, Platform: s.Platform, Source: s.Source, Generation: s.Generation, Scoped: s.Scoped, Sources: s.Sources, RoutingKey: s.RoutingKey})
	}
	return r.RunningCount(ctx, scopes, q)
}
