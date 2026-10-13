package autoapprove

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type sessionHandler func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform)

func (m *Module) withSession(h sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.Store == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		id := r.PathValue("id")
		if adapter := m.Session(w, r, id); adapter != nil {
			h(w, r, id, adapter)
		}
	}
}

// handleAutoApproveGet serves GET /api/session/{id}/auto-approve: the
// per-session override when set, otherwise the server-wide default.
func (m *Module) handleAutoApproveGet(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	enabled, exists, err := m.Store.GetAutoApprove(r.Context(), string(adapter.ID()), sessionID)
	if err != nil {
		httpx.ServerError(w, "getting auto-approve state", err)
		return
	}
	if !exists {
		enabled = m.DefaultEnabled()
	}
	httpx.WriteJSON(w, map[string]any{"enabled": enabled, "overridden": exists})
}

// handleAutoApproveSet serves POST /api/session/{id}/auto-approve with body
// {"enabled": bool}. Enabling also judges prompts already pending: their
// permission.asked event fired while auto-approve was off and will not
// repeat, so without this the prompt would wait on "starting…" forever.
func (m *Module) handleAutoApproveSet(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if err := m.Store.SetAutoApprove(r.Context(), string(adapter.ID()), sessionID, req.Enabled); err != nil {
		httpx.ServerError(w, "setting auto-approve state", err)
		return
	}
	if req.Enabled {
		m.resumePending(r.Context(), adapter, sessionID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleApprovedPermissions serves GET /api/session/{id}/approved-permissions:
// the approvals the conversation re-injects as notices after a refresh. A
// user's one-time approval is audit-only and stays out.
func (m *Module) handleApprovedPermissions(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	approved, err := m.Store.ListApprovedPermissions(r.Context(), string(adapter.ID()), sessionID)
	if err != nil {
		httpx.ServerError(w, "listing approved permissions", err)
		return
	}
	type entry struct {
		PermissionID string   `json:"permissionId"`
		Permission   string   `json:"permission"`
		Patterns     []string `json:"patterns"`
		Reasoning    string   `json:"reasoning"`
		ApprovedAt   int64    `json:"approvedAt"`
	}
	out := make([]entry, 0, len(approved))
	for _, p := range approved {
		if p.UserApproved() && p.Reply != state.ApprovalReplyAlways {
			continue
		}
		patterns := p.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		out = append(out, entry{PermissionID: p.PermissionID, Permission: p.PermissionText, Patterns: patterns, Reasoning: p.Reasoning, ApprovedAt: p.ApprovedAt})
	}
	httpx.WriteJSON(w, out)
}

// handlePromptSections serves GET/POST /api/settings/prompt-sections: the
// judge prompt's user sections.
func (m *Module) handlePromptSections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sections := []state.PromptSection{}
		if m.Store != nil {
			stored, err := m.Store.GetPromptSections(r.Context())
			if err != nil {
				httpx.ServerError(w, "reading prompt sections", err)
				return
			}
			if stored != nil {
				sections = stored
			}
		}
		httpx.WriteJSON(w, sections)
	case http.MethodPost:
		var sections []state.PromptSection
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &sections) {
			return
		}
		if m.Store == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		if err := m.Store.SetPromptSections(r.Context(), sections); err != nil {
			httpx.ServerError(w, "saving prompt sections", err)
			return
		}
		if sections == nil {
			sections = []state.PromptSection{}
		}
		httpx.WriteJSON(w, sections)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJudgeDelay serves GET/POST /api/settings/judge-delay ({"delayMs": n}).
func (m *Module) handleJudgeDelay(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var ms int64 = state.DefaultJudgeDelayMs
		if m.Store != nil {
			if d, err := m.Store.GetJudgeDelayMs(r.Context()); err == nil {
				ms = d
			}
		}
		httpx.WriteJSON(w, map[string]int64{"delayMs": ms})
	case http.MethodPost:
		var body struct {
			DelayMs int64 `json:"delayMs"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		if m.Store == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		if err := m.Store.SetJudgeDelayMs(r.Context(), body.DelayMs); err != nil {
			httpx.ServerError(w, "saving judge delay", err)
			return
		}
		// The next permission event uses the new delay without a DB read.
		m.svc.SetJudgeDelayMs(body.DelayMs)
		httpx.WriteJSON(w, map[string]int64{"delayMs": body.DelayMs})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJudgeModel serves GET/POST /api/settings/judge-model
// ({"model": "provider/modelID"}); an empty model reverts to the default.
func (m *Module) handleJudgeModel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var model string
		if m.Store != nil {
			if v, ok, err := m.Store.GetSetting(r.Context(), JudgeModelSettingKey); err == nil && ok {
				model = v
			}
		}
		httpx.WriteJSON(w, map[string]string{"model": model})
	case http.MethodPost:
		var body struct {
			Model string `json:"model"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		if m.Store == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		if err := m.Store.SetSetting(r.Context(), JudgeModelSettingKey, body.Model); err != nil {
			httpx.ServerError(w, "saving judge model", err)
			return
		}
		// Apply immediately so the next judge run uses the new model.
		m.svc.ReloadJudgeModel(r.Context())
		httpx.WriteJSON(w, map[string]string{"model": body.Model})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJudgeModelOptions serves GET /api/settings/judge-model/options: the
// selectable models plus the built-in default. An empty list means no
// OpenCode instance answered; the field stays free-text.
func (m *Module) handleJudgeModelOptions(w http.ResponseWriter, r *http.Request) {
	var models []string
	if m.openCode() { // the options come from running OpenCode instances
		models = m.ModelOptions(r.Context())
	}
	if models == nil {
		models = []string{}
	}
	httpx.WriteJSON(w, map[string]any{"models": models, "default": DefaultJudgeModel})
}

// handleJudgeEndpoint serves GET/POST /api/settings/judge-endpoint. The API
// key is write-only, and survives a save only while the endpoint is unchanged.
func (m *Module) handleJudgeEndpoint(w http.ResponseWriter, r *http.Request) {
	if m.Store == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	config, err := LoadJudgeEndpoint(r.Context(), m.Store)
	if err != nil {
		httpx.ServerError(w, "reading reviewer endpoint", err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJudgeEndpoint(w, config)
	case http.MethodPost:
		var body struct {
			Format             string  `json:"format"`
			Endpoint           string  `json:"endpoint"`
			Model              string  `json:"model"`
			APIKey             *string `json:"apiKey"`
			MinSafeProbability float64 `json:"minSafeProbability"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		next := JudgeEndpoint{Format: body.Format, Endpoint: strings.TrimSpace(body.Endpoint), Model: strings.TrimSpace(body.Model), APIKey: config.APIKey, MinSafeProbability: body.MinSafeProbability}
		if next.Endpoint != config.Endpoint {
			next.APIKey = ""
		}
		if body.APIKey != nil {
			next.APIKey = *body.APIKey
		}
		if err := next.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := json.Marshal(next)
		if err != nil {
			httpx.ServerError(w, "encoding reviewer endpoint", err)
			return
		}
		if err := m.Store.SetSetting(r.Context(), JudgeEndpointSettingKey, string(value)); err != nil {
			httpx.ServerError(w, "saving reviewer endpoint", err)
			return
		}
		writeJudgeEndpoint(w, next)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJudgeEndpoint(w http.ResponseWriter, config JudgeEndpoint) {
	keySet := config.APIKey != ""
	config.APIKey = ""
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, struct {
		JudgeEndpoint
		APIKeySet bool `json:"apiKeySet"`
	}{config, keySet})
}
