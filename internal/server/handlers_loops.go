package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/loops"
)

// handleLoops dispatches /api/loops and /api/loops/{id}[/action]. Kept in
// one handler because net/http's ServeMux predates path patterns and the
// rest of this codebase routes sub-paths manually (see sessionSubRoutes).
//
// All loop endpoints are localhost-only (AD-8), consistent with the MCP /
// tmux / worktree endpoints.
func (s *Server) handleLoops(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/loops")
	rest = strings.TrimPrefix(rest, "/")

	switch {
	case rest == "":
		switch r.Method {
		case http.MethodGet:
			s.handleLoopsList(w, r)
		case http.MethodPost:
			s.handleLoopsCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	default:
		s.handleLoopByID(w, r, rest)
	}
}

// handleLoopByID handles /api/loops/{id} and /api/loops/{id}/{action}.
func (s *Server) handleLoopByID(w http.ResponseWriter, r *http.Request, rest string) {
	id, action, _ := strings.Cut(rest, "/")
	ctx := r.Context()
	svc := s.loopSvc()

	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			detail, err := svc.Get(ctx, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			writeJSON(w, detail)
		case http.MethodPatch:
			s.handleLoopUpdate(w, r, svc, id)
		case http.MethodDelete:
			if err := svc.Delete(ctx, id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "restart":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		view, err := svc.Restart(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, view)
	case "pause", "resume", "step", "trigger":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleLoopControl(w, r, svc, id, action)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleLoopControl(w http.ResponseWriter, r *http.Request, svc *loops.Service, id, action string) {
	ctx := r.Context()
	var err error
	switch action {
	case "pause":
		err = svc.Pause(ctx, id)
	case "resume":
		err = svc.Resume(ctx, id)
	case "step":
		err = svc.Step(ctx, id)
	case "trigger":
		err = svc.TriggerNow(ctx, id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLoopUpdate(w http.ResponseWriter, r *http.Request, svc *loops.Service, id string) {
	var upd loops.LoopUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	view, err := svc.Update(r.Context(), id, upd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, view)
}

func (s *Server) handleLoopsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	views, err := s.loopSvc().List(r.Context(), loops.LoopFilter{
		RootSessionID: q.Get("session"),
		Directory:     q.Get("dir"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, views)
}

func (s *Server) handleLoopsCreate(w http.ResponseWriter, r *http.Request) {
	var spec loops.LoopSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	view, err := s.loopSvc().Create(r.Context(), spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, view)
}
