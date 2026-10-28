package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ocman/ocman/internal/db"
)

// broadcastGitCommand sends identity and a refresh hint, never shell arguments
// or output. A failed command may still have changed the repository.
func (s *Server) broadcastGitCommand(ctx context.Context, platformID, sessionID, action string) {
	if sessionID == "" || (action != "push" && action != "commit") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var session *db.Session
	if adapter, ok := s.metadataAdapter(ctx, platformID, sessionID); ok {
		// Hints must never trigger transcript reads or RPCs in the stream.
		if cached, ok := adapter.(interface {
			CachedSession(string) (db.Session, bool)
		}); ok {
			if row, found := cached.CachedSession(sessionID); found {
				session = &row
			}
		}
	}
	if session == nil {
		return
	}
	owner := session.RemoteID
	if owner == "" {
		owner = "local"
	}
	payload, _ := json.Marshal(map[string]string{
		"sessionID": sessionID, "action": action, "remoteId": owner,
		"projectId": session.ProjectID, "directory": session.Directory,
	})
	s.broadcastGlobalEvent("ocman.git.command", payload)
}
