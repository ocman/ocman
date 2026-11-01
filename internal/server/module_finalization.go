package server

import "github.com/ocman/ocman/internal/module"

// finalizeLocalHost cancels and joins the local host's detached work
// (worktree naming, shared runtime launches) after workers join and before
// the caller closes state.db.
func (s *Server) finalizeLocalHost() {
	if f, ok := s.router().Local().(module.Finalizer); ok {
		f.Finalize()
	}
}

func finalizeModules(mods []module.Module) {
	for i := len(mods) - 1; i >= 0; i-- {
		if f, ok := mods[i].(module.Finalizer); ok {
			f.Finalize()
		}
	}
}
