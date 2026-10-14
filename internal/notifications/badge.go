package notifications

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

type BadgeSettings struct {
	Sessions bool `json:"sessions"`
	Primary  bool `json:"primary"`
	Factory  bool `json:"factory"`
	Routines bool `json:"routines"`
}

func (m *Module) BadgeSettings(ctx context.Context) (BadgeSettings, error) {
	settings := BadgeSettings{Sessions: true, Primary: true, Factory: true}
	if m.d.Store == nil {
		return settings, nil
	}
	value, ok, err := m.d.Store.GetSetting(ctx, "notifications.badge")
	if err != nil || !ok {
		return settings, err
	}
	err = json.Unmarshal([]byte(value), &settings)
	return settings, err
}

func (m *Module) badgeSettings(w http.ResponseWriter, r *http.Request) {
	if !m.available(w, r) {
		return
	}
	if r.Method == http.MethodPost {
		m.store.mu.Lock()
		defer m.store.mu.Unlock()
	}
	settings, err := m.BadgeSettings(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading app badge settings", err)
		return
	}
	if r.Method == http.MethodPost {
		input := &settings
		if !httpx.ReadJSON(w, r, 4096, &input) {
			return
		}
		if input == nil {
			http.Error(w, "badge settings required", http.StatusBadRequest)
			return
		}
		data, _ := json.Marshal(input) // Fixed boolean fields cannot fail to marshal.
		err = m.d.Store.SetSetting(r.Context(), "notifications.badge", string(data))
		if err != nil {
			httpx.ServerError(w, "saving app badge settings", err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, settings)
}
