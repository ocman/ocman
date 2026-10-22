package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionlifecycle"
)

type newSessionTarget = sessionlifecycle.Target

func (s *Server) ensureProjectForCreate(w http.ResponseWriter, r *http.Request, platform, directory string) (hostsvc.Host, string, bool) {
	return s.sessionLifecycleModule().EnsureProjectForCreate(w, r, platform, directory)
}

func remoteIDForPlatform(platform string) string {
	if remoteID, _ := platforms.SplitPlatformID(platform); remoteID != "" {
		return remoteID
	}
	return "local"
}
