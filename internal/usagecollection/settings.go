package usagecollection

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/usagewire"
)

type settingsView struct {
	Enabled  bool   `json:"enabled"`
	RelayURL string `json:"relayUrl"`
	Version  string `json:"version"`
}

func (m *Module) enabled(ctx context.Context) (bool, error) {
	value, ok, err := m.deps.Store.GetSetting(ctx, EnabledSetting)
	if err != nil || !ok {
		return err == nil, err
	}
	return strconv.ParseBool(value) // Invalid configuration fails closed.
}

func (m *Module) Routes(router module.Router) {
	router.Setting("GET /api/settings/usage-collection", m.settings)
	router.Setting("POST /api/settings/usage-collection", m.settings)
}

func (m *Module) settings(w http.ResponseWriter, r *http.Request) {
	if m.deps.Store == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if r.Method == http.MethodPost {
		if !usagewire.ReadJSON(w, r, &body) {
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Method == http.MethodPost {
		if body.Enabled == nil {
			http.Error(w, "enabled is required", http.StatusBadRequest)
			return
		}
		if err := m.deps.Store.SetSetting(r.Context(), EnabledSetting, strconv.FormatBool(*body.Enabled)); err != nil {
			httpx.ServerError(w, "saving usage collection", err)
			return
		}
		if !*body.Enabled {
			if m.cancel != nil {
				m.cancel()
			}
			if err := m.clearPending(r.Context()); err != nil {
				httpx.ServerError(w, "clearing pending usage", err)
				return
			}
		}
		m.nextAttempt = time.Time{}
	}
	enabled, err := m.enabled(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading usage collection", err)
		return
	}
	httpx.WriteJSON(w, settingsView{Enabled: enabled, RelayURL: endpoint(m.deps.RelayURL()), Version: usagewire.NormalizeVersion(m.deps.Version())})
}
