package server

import (
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s *Server) handleProjectBeadsStatus(w http.ResponseWriter, r *http.Request) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}

	host := s.router().ForDir(dir)
	if remoteID := r.URL.Query().Get("remoteId"); remoteID != "" {
		host = s.router().ForRemote(remoteID)
		if remoteID != "local" && host.RemoteID() != remoteID {
			http.Error(w, "unknown remote owner", http.StatusBadRequest)
			return
		}
	}

	status, err := host.BeadsStatus(r.Context(), dir)
	if err != nil {
		log.WithError(err).Warn("beads status failed")
		http.Error(w, "failed to read Beads status", http.StatusBadGateway)
		return
	}
	writeJSON(w, status)
}
