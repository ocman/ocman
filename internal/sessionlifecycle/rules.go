package sessionlifecycle

import (
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// HandleSessionPermissionRulesGet returns an empty list for inherited defaults.
func (m *Module) HandleSessionPermissionRulesGet(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
		rules, err := adapter.PermissionRules(r.Context(), sessionID)
		if err != nil {
			httpx.WritePlatformError(w, "reading permission rules", err)
			return
		}
		if rules == nil {
			rules = []platforms.PermissionRule{}
		}
		response := map[string]any{"rules": rules}
		if adapter.Capabilities().NativeAutoPermission {
			if native, ok := adapter.(platforms.NativePermissions); ok {
				state, err := native.NativePermissionState(r.Context(), sessionID)
				if err != nil && !errors.Is(err, platforms.ErrNotFound) && !errors.Is(err, platforms.ErrUnsupported) {
					httpx.WritePlatformError(w, "reading native permission mode", err)
					return
				}
				if err == nil {
					response["nativeMode"] = state
				}
			}
		}
		httpx.WriteJSON(w, response)
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
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	if err := m.d.Sessions().SetPermissionRules(r.Context(), platformHint(r), platforms.SetPermissionRulesRequest{
		SessionID: sessionID,
		Rules:     req.Rules,
	}); err != nil {
		writeSessionError(w, "setting permission rules", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
