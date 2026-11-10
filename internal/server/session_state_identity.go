package server

import (
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// sessionStatePlatform keeps durable flags on the owner/native-kind namespace.
// A connection routing key is wire identity, not a new namespace for old flags.
// This registry-only mapping adds no per-row catalogue queries to list overlays.
func (s *Server) sessionStatePlatform(route string) string {
	if s == nil || s.registry == nil {
		return route
	}
	p, ok := s.registry.Get(platforms.ID(route))
	if !ok {
		return route
	}
	kind := eventNativeKind(p)
	owner, _ := platforms.SplitPlatformID(route)
	if owner != "" {
		return platforms.CompoundPlatformID(owner, kind)
	}
	return kind
}

// Workflow associations may explicitly pin a connection. Prefer that identity
// while retaining migrated native-key associations; only display flags require
// the native namespace unconditionally.
func (s *Server) sessionAssociationID(tags map[state.Key]string, route, id string) string {
	if value := tags[state.Key{Platform: route, SessionID: id}]; value != "" {
		return value
	}
	return tags[state.Key{Platform: s.sessionStatePlatform(route), SessionID: id}]
}
