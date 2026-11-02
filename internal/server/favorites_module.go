package server

import (
	"github.com/ocman/ocman/internal/favorites"
	"github.com/ocman/ocman/internal/platforms"
)

// favoritesModule is stateless, so it is rebuilt on each call.
func (s *Server) favoritesModule() *favorites.Module {
	d := favorites.Deps{Registered: s.platformRegistered}
	if s.stateDB != nil {
		d.Store = s.stateDB // untyped nil otherwise
	}
	return favorites.NewModule(d)
}

// platformRegistered reports whether id names a registered platform.
func (s *Server) platformRegistered(id string) bool {
	if s.registry == nil {
		return false
	}
	_, ok := s.registry.Get(platforms.ID(id))
	return ok
}
