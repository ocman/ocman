package server

import "github.com/NoUseFreak/ocman/internal/db"

func (s *Server) broadcastSessionStatus(platform, sessionID string, status db.SessionStatus) {
	s.broadcastSessionPatch(platform, sessionID, map[string]interface{}{"status": status})
}
