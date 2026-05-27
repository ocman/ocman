package server

import (
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
