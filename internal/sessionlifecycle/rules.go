package sessionlifecycle

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// HandleSessionPermissionRulesGet returns an empty list for inherited defaults.
func (m *Module) HandleSessionPermissionRulesGet(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		rules, err := adapter.PermissionRules(r.Context(), sessionID)
		if err != nil {
			httpx.WritePlatformError(w, "reading permission rules", err)
			return
		}
		if rules == nil {
			rules = []platforms.PermissionRule{}
		}
		httpx.WriteJSON(w, map[string]any{"rules": rules})
	})
}

// HandleSessionPermissionRulesSet replaces rules; an empty list restores defaults.
func (m *Module) HandleSessionPermissionRulesSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []platforms.PermissionRule `json:"rules"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().SetPermissionRules(r.Context(), platformHint(r), platforms.SetPermissionRulesRequest{
			SessionID: sessionID,
			Rules:     req.Rules,
		}); err != nil {
			writeSessionError(w, "setting permission rules", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
