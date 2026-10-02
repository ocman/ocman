package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/db"
)

const archiveResurfaceKey = "session.archive_resurface"

func (s *Server) archiveResurfaceMode(ctx context.Context) (string, error) {
	value, _, err := s.stateDB.GetSetting(ctx, archiveResurfaceKey)
	if value != "activity" {
		value = "halt"
	}
	return value, err
}

func shouldResurfaceSession(session db.Session, archivedAt int64, mode string) bool {
	if session.TimeUpdated <= archivedAt {
		return false
	}
	return mode == "activity" || session.Status != db.StatusBusy
}

func (s *Server) handleArchiveResurface(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		mode, err := s.archiveResurfaceMode(r.Context())
		if err != nil {
			serverError(w, "reading archive resurfacing setting", err)
			return
		}
		writeJSON(w, map[string]string{"mode": mode})
	case http.MethodPost:
		var body struct {
			Mode string `json:"mode"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		if body.Mode != "activity" && body.Mode != "halt" {
			http.Error(w, "mode must be activity or halt", http.StatusBadRequest)
			return
		}
		if err := s.stateDB.SetSetting(r.Context(), archiveResurfaceKey, body.Mode); err != nil {
			serverError(w, "saving archive resurfacing setting", err)
			return
		}
		writeJSON(w, body)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
