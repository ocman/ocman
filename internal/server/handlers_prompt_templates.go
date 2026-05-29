package server

import (
	"net/http"
)

// settingKey names for the prompt templates persisted in the
// state.db `setting` table (schema v12). Kept in package scope so
// tests can assert against the exact keys.
const (
	settingPRPromptTemplate    = "pr_prompt_template"
	settingIssuePromptTemplate = "issue_prompt_template"
)

// DefaultPRPromptTemplate is shipped with ocman and used when the
// user hasn't customised the PR template. Uses placeholders from
// forge.SupportedPlaceholders so the in-app reset button always
// produces a working prompt.
const DefaultPRPromptTemplate = `Please handle PR #{number}: {title}

Author: {author}
Link: {url}
Branch: {branch}

Description:
{body}`

// DefaultIssuePromptTemplate is the issue counterpart. No {branch}
// placeholder because issues don't have a source branch.
const DefaultIssuePromptTemplate = `Please handle issue #{number}: {title}

Author: {author}
Link: {url}

Description:
{body}`

// promptTemplatesResponse is the JSON shape returned by both the
// GET and POST handlers. Keys match forge.SupportedPlaceholders'
// convention (lowercase, descriptive).
type promptTemplatesResponse struct {
	PR    string `json:"pr"`
	Issue string `json:"issue"`
}

// handlePromptTemplates dispatches GET and POST on
// /api/settings/prompt-templates.
//
// GET  → returns the currently stored templates (or built-in defaults
//
//	when nothing is stored). Missing keys default; explicit empty
//	strings are honoured.
//
// POST → accepts {pr, issue} and persists. Missing fields in the
//
//	request leave the stored value unchanged. Responds with the
//	updated payload.
func (s *Server) handlePromptTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetPromptTemplates(w, r)
	case http.MethodPost:
		s.handleSetPromptTemplates(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetPromptTemplates(w http.ResponseWriter, _ *http.Request) {
	out := promptTemplatesResponse{
		PR:    DefaultPRPromptTemplate,
		Issue: DefaultIssuePromptTemplate,
	}
	if s.stateDB != nil {
		if v, ok, err := s.stateDB.GetSetting(settingPRPromptTemplate); err == nil && ok {
			out.PR = v
		}
		if v, ok, err := s.stateDB.GetSetting(settingIssuePromptTemplate); err == nil && ok {
			out.Issue = v
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleSetPromptTemplates(w http.ResponseWriter, r *http.Request) {
	// Use pointer fields so we can distinguish "field absent" from
	// "field explicitly set to empty string" — only present fields
	// overwrite the stored value.
	var body struct {
		PR    *string `json:"pr"`
		Issue *string `json:"issue"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &body) {
		return
	}
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	if body.PR != nil {
		if err := s.stateDB.SetSetting(settingPRPromptTemplate, *body.PR); err != nil {
			serverError(w, "saving pr template", err)
			return
		}
	}
	if body.Issue != nil {
		if err := s.stateDB.SetSetting(settingIssuePromptTemplate, *body.Issue); err != nil {
			serverError(w, "saving issue template", err)
			return
		}
	}
	// Read-after-write so the response always reflects the persisted
	// state, not the (possibly partial) input.
	s.handleGetPromptTemplates(w, r)
}
