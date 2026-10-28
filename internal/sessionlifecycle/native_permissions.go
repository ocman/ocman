package sessionlifecycle

import (
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// A distinct mutation route makes older owners reject native-mode changes
// instead of interpreting an unknown field as an empty permission ruleset.
func (m *Module) HandleNativePermissionModeSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	id, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	if err := m.d.Sessions().SetPermissionRules(r.Context(), platformHint(r), platforms.SetPermissionRulesRequest{
		SessionID: id, NativeMode: &req.Mode,
	}); err != nil {
		writeSessionError(w, "setting native permission mode", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
