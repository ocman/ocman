package server

import (
	"net/http"
)

func (s *Server) handleFactoryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.factory.Status(r.Context()))
}
