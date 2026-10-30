package platformsettings

import (
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
)

func (m *Module) checkSetup(w http.ResponseWriter, r *http.Request)   { m.platformSetup(w, r, false) }
func (m *Module) installSetup(w http.ResponseWriter, r *http.Request) { m.platformSetup(w, r, true) }

func (m *Module) platformSetup(w http.ResponseWriter, r *http.Request, install bool) {
	if r.PathValue("platform") != "codex" {
		http.Error(w, "unsupported platform setup", http.StatusBadRequest)
		return
	}
	if m.setup == nil {
		http.Error(w, "platform setup unavailable", http.StatusServiceUnavailable)
		return
	}
	host := m.setup()
	if host == nil {
		http.Error(w, "platform setup unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := host.CheckPlatformSetup(r.Context(), r.PathValue("platform"))
	if err == nil && install {
		status, err = host.InstallPlatformSetup(r.Context(), r.PathValue("platform"))
	}
	if err != nil {
		httpx.ServerError(w, "platform setup", err)
		return
	}
	httpx.WriteJSON(w, status)
}
