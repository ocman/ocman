package server

import "net/http"

func (s *Server) handleDaguStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.router().ForRemote(r.URL.Query().Get("remoteId")).DaguStatus(r.Context()))
}
