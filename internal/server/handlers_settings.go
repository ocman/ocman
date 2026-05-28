package server

import (
	"encoding/json"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/state"
)

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
	s.judgeDelayMs = body.DelayMs
	writeJSON(w, map[string]int64{"delayMs": body.DelayMs})
}
