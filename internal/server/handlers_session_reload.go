package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s *Server) handleSessionReloadOpencode(w http.ResponseWriter, r *http.Request) {
	if !s.isPrivilegedRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session", err)
			return
		}
		if detail == nil || detail.Session == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		owner := detail.Session.RemoteID
		if owner == "" {
			owner = "local"
		}
		host, ok := s.resolveOwner(w, detail.Session.Directory, owner)
		if !ok {
			return
		}
		if err := host.ReloadOpencode(r.Context()); err != nil {
			writePlatformError(w, "reloading OpenCode configuration", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
