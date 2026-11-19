package server

import (
	"github.com/ocman/ocman/internal/favorites"
	"github.com/ocman/ocman/internal/platforms"
)

// favoritesModule is stateless, so it is rebuilt on each call.
func (s *Server) favoritesModule() *favorites.Module {
	d := favorites.Deps{ResolvePlatform: s.favoritePlatform}
	if s.stateDB != nil {
		d.Store = s.stateDB // untyped nil otherwise
	}
	return favorites.NewModule(d)
}

// Favorites retain the native namespace read by model catalogs, even when
// the picker sends a named connection. Legacy native names remain valid while
// a matching adapter is registered, without selecting an execution account.
func (s *Server) favoritePlatform(id string) (string, bool) {
	if s.registry == nil {
		return "", false
	}
	if _, ok := s.registry.Get(platforms.ID(id)); ok {
		return s.sessionStatePlatform(id), true
	}
	for _, entry := range s.registry.Entries() {
		if s.sessionStatePlatform(string(entry.Key)) == id {
			return id, true
		}
	}
	return "", false
}

// platformRegistered reports whether id names a registered platform.
func (s *Server) platformRegistered(id string) bool {
	if s.registry == nil {
		return false
	}
	_, ok := s.registry.Get(platforms.ID(id))
	return ok
}
