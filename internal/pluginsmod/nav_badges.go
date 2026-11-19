package pluginsmod

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/remote"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func (m *Module) handlePluginNavBadges(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("ownerId")
	if owner == "" {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	writePluginResponse(w, m.RouteOperation(ctx, owner, remote.PluginRequest{Operation: "nav-badges", Read: true}))
}

// localPluginNavBadges reads live snapshots without calling any plugin. Only
// processes with a badge need a durable authorization check.
func (m *Module) localPluginNavBadges(ctx context.Context, fromRemote bool) (map[string]plugins.NavBadge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil, plugins.ErrUnavailable
	}
	badges := make(map[string]plugins.NavBadge)
	for id, process := range m.processes {
		if _, ok := process.NavigationBadge(); !ok {
			continue
		}
		err := m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
			if d.NavBadgeAllowed(grants) && (!fromRemote || d.Scope == plugins.ScopeOwner) {
				if badge, ok := process.NavigationBadge(); ok {
					badges[id] = badge
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, plugins.ErrUnavailable) {
			return nil, err
		}
	}
	return badges, nil
}
