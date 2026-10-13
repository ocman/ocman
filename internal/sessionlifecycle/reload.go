package sessionlifecycle

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (m *Module) HandleSessionReloadOpencode(w http.ResponseWriter, r *http.Request) {
	if !m.d.Privileged(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			httpx.WritePlatformError(w, "loading session", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		owner := detail.Session.RemoteID
		if owner == "" {
			owner = "local"
		}
		host, ok := m.d.Owner(w, detail.Session.Directory, owner)
		if !ok {
			return
		}
		if err := host.ReloadOpencode(r.Context()); err != nil {
			httpx.WritePlatformError(w, "reloading OpenCode configuration", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
