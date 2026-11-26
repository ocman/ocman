package server

import (
	"path/filepath"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/state"
)

func (s *Server) sidebarUpstreams(owner, dir string) ([]forge.Remote, bool) {
	if _, connected := s.router().LookupRemote(owner); !connected || s.remotes == nil {
		return nil, false
	}
	return s.remotes.CachedForgeRemotes(owner, filepath.Clean(dir), state.ProjectRootForDirectory(filepath.Clean(dir)))
}
