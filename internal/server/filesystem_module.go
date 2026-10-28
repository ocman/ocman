package server

import (
	"net/http"

	"github.com/ocman/ocman/internal/filesystem"
	"github.com/ocman/ocman/internal/httpx"
)

// filesystemModule exists without a state DB too: browsing and repository
// reads need none, and signed files then fail as before.
func (s *Server) filesystemModule() *filesystem.Module {
	return s.filesystemMod.get(func() *filesystem.Module {
		return filesystem.NewModule(filesystem.Deps{
			Store: s.stateDB,
			Owner: s.repoFilesOwner,
		})
	})
}

// repoFilesOwner requires an explicit remoteId, so a disconnected remote
// fails closed (503) instead of reading the hub's checkout.
func (s *Server) repoFilesOwner(w http.ResponseWriter, r *http.Request) (string, filesystem.RepoFiles, bool) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return "", nil, false
	}
	remoteID, ok := httpx.RemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return "", nil, false
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return "", nil, false
	}
	return dir, host, true
}
