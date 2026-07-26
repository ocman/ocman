package server

import "net/http"

// Dagu is the workflow runner, so ocman starts and observes runs itself
// through the workflow service rather than exposing them as their own
// API. Only availability is surfaced: the UI tells the user to install
// Dagu when workflows would otherwise not run.
func (s *Server) handleDaguStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.router().ForRemote(r.URL.Query().Get("remoteId")).DaguStatus(r.Context()))
}
