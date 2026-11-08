// Package resourcemod provides read-only owner-scoped process resources.
package resourcemod

import (
	"context"
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type Module struct {
	ResolveOwner func(http.ResponseWriter, string, string) (hostsvc.Host, bool)
	Sessions     func(context.Context, string, int, int) ([]sessioncatalog.Session, error)
}

func (*Module) Name() string { return "resources" }

func (m *Module) Routes(router module.Router) {
	router.Handle("GET /api/resources", m.resources)
}

func (m *Module) resources(w http.ResponseWriter, r *http.Request) {
	owner, ok := m.ResolveOwner(w, "", r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	viewer, ok := owner.(hostsvc.ResourceViewer)
	if !ok {
		http.Error(w, "resource viewer unavailable on this host", http.StatusServiceUnavailable)
		return
	}
	snapshot, err := viewer.Resources(r.Context())
	if errors.Is(err, platforms.ErrUnsupported) {
		http.Error(w, "resource viewer unavailable on this host", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "could not read process resources", http.StatusServiceUnavailable)
		return
	}
	if m.Sessions != nil {
		snapshot.Processes = m.sessionLinks(r.Context(), owner.RemoteID(), snapshot.Processes)
	}
	httpx.WriteJSON(w, snapshot)
}
