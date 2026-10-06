package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
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
	if !isRemotePlatformID(platformID) && s.db != nil {
		session, _ = s.db.GetSession(ctx, sessionID)
	} else if s.registry != nil {
		if adapter, ok := s.registry.Get(platforms.ID(platformID)); ok {
			if detail, err := adapter.Session(ctx, sessionID, 1, 0); err == nil && detail != nil {
				session = detail.Session
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
