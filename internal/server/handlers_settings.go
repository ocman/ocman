package server

import (
	"encoding/json"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/state"
)

// remoteAccessStatus is the GET /api/settings/remote-access response. It
// describes this instance's own remote-access surface so the operator can
// copy the connection details into a hub. The token itself is never
// included; the masked indicator + explicit reveal action handle that.
type remoteAccessStatus struct {
	InstanceID string `json:"instanceId"`
	Listening  bool   `json:"listening"`
	ListenAddr string `json:"listenAddr"`
	TLS        bool   `json:"tls"`
	TokenSet   bool   `json:"tokenSet"`
}

// handleRemoteAccess handles GET /api/settings/remote-access — this
// instance's identity and gRPC-listen status. Auth-gated; no plaintext
// token (AD-5).
func (s *Server) handleRemoteAccess(w http.ResponseWriter, _ *http.Request) {
	status := remoteAccessStatus{
		InstanceID: s.remoteAccess.instanceID,
		Listening:  s.remoteAccess.listening,
		ListenAddr: s.remoteAccess.listenAddr,
		TLS:        s.remoteAccess.tls,
	}
	if s.stateDB != nil {
		if ident, err := s.stateDB.InstanceIdentity(); err == nil {
			if status.InstanceID == "" {
				status.InstanceID = ident.InstanceID
			}
			status.TokenSet = ident.RemoteToken != ""
		}
	}
	writeJSON(w, status)
}

// handleRevealRemoteToken handles POST /api/settings/remote-access/reveal-token
// — the explicit, authenticated action that returns this instance's own
// plaintext remote-access token for copy-to-clipboard. It never returns
// tokens stored for attached remotes (AD-5, NFR-4).
func (s *Server) handleRevealRemoteToken(w http.ResponseWriter, _ *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	ident, err := s.stateDB.InstanceIdentity()
	if err != nil {
		serverError(w, "reading instance identity", err)
		return
	}
	writeJSON(w, map[string]string{"token": ident.RemoteToken})
}

// handlePromptSections handles GET and POST /api/settings/prompt-sections.
//
// GET  → returns the currently stored judge prompt sections as JSON.
// POST → replaces all sections with the request body; responds with the
//
//	updated list.
func (s *Server) handlePromptSections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetPromptSections(w, r)
	case http.MethodPost:
		s.handleSetPromptSections(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetPromptSections(w http.ResponseWriter, _ *http.Request) {
	if s.stateDB == nil {
		writeJSON(w, []state.PromptSection{})
		return
	}
	sections, err := s.stateDB.GetPromptSections()
	if err != nil {
		serverError(w, "reading prompt sections", err)
		return
	}
	if sections == nil {
		sections = []state.PromptSection{}
	}
	writeJSON(w, sections)
}

func (s *Server) handleSetPromptSections(w http.ResponseWriter, r *http.Request) {
	var sections []state.PromptSection
	if !readAndUnmarshal(w, r, maxRequestBody, &sections) {
		return
	}
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.stateDB.SetPromptSections(sections); err != nil {
		serverError(w, "saving prompt sections", err)
		return
	}
	if sections == nil {
		sections = []state.PromptSection{}
	}
	writeJSON(w, sections)
}

// handleJudgeDelay handles GET and POST /api/settings/judge-delay.
//
// GET  → returns {"delayMs": <n>}
// POST → accepts {"delayMs": <n>}, persists, responds with the updated value.
func (s *Server) handleJudgeDelay(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetJudgeDelay(w, r)
	case http.MethodPost:
		s.handleSetJudgeDelay(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetJudgeDelay(w http.ResponseWriter, _ *http.Request) {
	var ms int64 = state.DefaultJudgeDelayMs
	if s.stateDB != nil {
		if d, err := s.stateDB.GetJudgeDelayMs(); err == nil {
			ms = d
		}
	}
	writeJSON(w, map[string]int64{"delayMs": ms})
}

func (s *Server) handleSetJudgeDelay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DelayMs int64 `json:"delayMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.stateDB.SetJudgeDelayMs(body.DelayMs); err != nil {
		serverError(w, "saving judge delay", err)
		return
	}
	// Keep the in-memory cache in sync so the next permission event uses
	// the updated delay without a DB round-trip.
	s.aaSvc().SetJudgeDelayMs(body.DelayMs)
	writeJSON(w, map[string]int64{"delayMs": body.DelayMs})
}

// handleJudgeModel handles GET and POST /api/settings/judge-model.
//
// GET  → returns {"model": "provider/modelID"} — empty string when
//
//	unset (the backend then uses its built-in default).
//
// POST → accepts {"model": "provider/modelID"}, persists it, and
//
//	responds with the stored value. An empty string clears the
//	setting, reverting to the default.
func (s *Server) handleJudgeModel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetJudgeModel(w, r)
	case http.MethodPost:
		s.handleSetJudgeModel(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetJudgeModel(w http.ResponseWriter, _ *http.Request) {
	var model string
	if s.stateDB != nil {
		if v, ok, err := s.stateDB.GetSetting(autoapprove.JudgeModelSettingKey); err == nil && ok {
			model = v
		}
	}
	writeJSON(w, map[string]string{"model": model})
}

func (s *Server) handleSetJudgeModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.stateDB.SetSetting(autoapprove.JudgeModelSettingKey, body.Model); err != nil {
		serverError(w, "saving judge model", err)
		return
	}
	// Apply immediately so the next judge run uses the new model.
	s.aaSvc().ReloadJudgeModel()
	writeJSON(w, map[string]string{"model": body.Model})
}

// handleWorktreeInheritPermissions handles GET and POST
// /api/settings/worktree-inherit-permissions (issue #101).
//
// GET  → {"enabled": bool}. Defaults to enabled when unset.
// POST → accepts {"enabled": bool}, persists, returns the new state.
func (s *Server) handleWorktreeInheritPermissions(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		on, err := s.stateDB.GetWorktreeInheritPermissions()
		if err != nil {
			serverError(w, "reading worktree inherit-permissions setting", err)
			return
		}
		writeJSON(w, map[string]bool{"enabled": on})
	case http.MethodPost:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		if err := s.stateDB.SetWorktreeInheritPermissions(body.Enabled); err != nil {
			serverError(w, "saving worktree inherit-permissions setting", err)
			return
		}
		writeJSON(w, map[string]bool{"enabled": body.Enabled})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
