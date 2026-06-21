package server

import (
	"net/http"
	"strconv"
	"strings"
)

// handlers_remotes.go implements the hub-side /api/remotes management API
// for multi-remote support (FR-14). Tokens are accepted on write but
// never returned; the list view shows health/counts only.

// handleRemotes routes /api/remotes (collection) for GET (list) and
// POST (add). The state DB and a remote manager are required.
func (s *Server) handleRemotes(w http.ResponseWriter, r *http.Request) {
	if s.remotes == nil {
		// Multi-remote not active: report an empty list rather than an
		// error so the settings page renders cleanly on single-host.
		if r.Method == http.MethodGet {
			writeJSON(w, []any{})
			return
		}
		http.Error(w, "remote management not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleRemotesList(w, r)
	case http.MethodPost:
		s.handleRemotesAdd(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRemotesList(w http.ResponseWriter, _ *http.Request) {
	list, err := s.remotes.List()
	if err != nil {
		serverError(w, "listing remotes", err)
		return
	}
	if list == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, list)
}

type addRemoteRequest struct {
	Address     string `json:"address"`
	Token       string `json:"token"`
	DisplayName string `json:"displayName"`
}

func (s *Server) handleRemotesAdd(w http.ResponseWriter, r *http.Request) {
	var req addRemoteRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Token = strings.TrimSpace(req.Token)
	if req.Address == "" || req.Token == "" {
		http.Error(w, "address and token are required", http.StatusBadRequest)
		return
	}
	id, err := s.remotes.Add(r.Context(), req.Address, req.Token, strings.TrimSpace(req.DisplayName))
	if err != nil {
		serverError(w, "adding remote", err)
		return
	}
	// Return the freshly-created row's status. The dial happens in the
	// background; the initial health is "connecting".
	list, _ := s.remotes.List()
	for _, st := range list {
		if st.LocalID == id {
			writeJSON(w, st)
			return
		}
	}
	writeJSON(w, map[string]int64{"localId": id})
}

// handleRemoteByID routes /api/remotes/{localId} and its subpaths
// (/reconnect). PUT edits, DELETE removes, POST .../reconnect reconnects.
func (s *Server) handleRemoteByID(w http.ResponseWriter, r *http.Request) {
	if s.remotes == nil {
		http.Error(w, "remote management not available", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/remotes/")
	idStr, action, _ := strings.Cut(rest, "/")
	localID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid remote id", http.StatusBadRequest)
		return
	}

	switch action {
	case "reconnect":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.remotes.Reconnect(r.Context(), localID); err != nil {
			serverError(w, "reconnecting remote", err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "":
		switch r.Method {
		case http.MethodPut:
			s.handleRemoteUpdate(w, r, localID)
		case http.MethodDelete:
			if err := s.remotes.Remove(localID); err != nil {
				serverError(w, "removing remote", err)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

type updateRemoteRequest struct {
	DisplayName string  `json:"displayName"`
	Address     string  `json:"address"`
	Enabled     bool    `json:"enabled"`
	Token       *string `json:"token"` // nil = leave unchanged
}

func (s *Server) handleRemoteUpdate(w http.ResponseWriter, r *http.Request, localID int64) {
	var req updateRemoteRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}
	var token *string
	if req.Token != nil {
		t := strings.TrimSpace(*req.Token)
		if t != "" {
			token = &t
		}
	}
	if err := s.remotes.Update(r.Context(), localID, strings.TrimSpace(req.DisplayName), req.Address, req.Enabled, token); err != nil {
		serverError(w, "updating remote", err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
