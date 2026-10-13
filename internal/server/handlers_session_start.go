package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionlifecycle"
)

type newSessionTarget = sessionlifecycle.Target

func (s *Server) handlePrepareSession(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandlePrepareSession(w, r)
}

func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleStartSession(w, r)
}

func (s *Server) ensureProjectForCreate(w http.ResponseWriter, r *http.Request, platform, directory string) (hostsvc.Host, string, bool) {
	return s.sessionLifecycleModule().EnsureProjectForCreate(w, r, platform, directory)
}

func remoteIDForPlatform(platform string) string {
	if remoteID, _ := remote.SplitPlatformID(platform); remoteID != "" {
		return remoteID
	}
	return "local"
}
