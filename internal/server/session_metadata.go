package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
)

func (s *Server) metadataAdapter(ctx context.Context, scope, id string) (platforms.Platform, bool) {
	if identity.IsPendingReference(id) {
		return nil, false
	}
	if s.registry != nil {
		if scope != "" {
			if adapter, ok := s.registry.Get(platforms.ID(scope)); ok {
				return adapter, true
			}
		} else if adapter, ok := s.registry.PlatformForSession(ctx, id); ok {
			return adapter, true
		}
		if s.registry.CanonicalSessionIDs() {
			return nil, false
		}
	}
	return nil, false
}

func (s *Server) sessionMetadata(ctx context.Context, scope, id string) (*db.Session, error) {
	if adapter, ok := s.metadataAdapter(ctx, scope, id); ok {
		if reader, ok := adapter.(platforms.SessionMetadataReader); ok {
			return reader.SessionMetadata(ctx, id)
		}
		return platforms.ReadSessionSummary(ctx, adapter, id)
	}
	return nil, platforms.ErrNotFound
}

func (s *Server) sessionDescendants(ctx context.Context, id string) ([]string, error) {
	adapter, ok := s.metadataAdapter(ctx, "", id)
	if !ok {
		if s.registry == nil && s.db == nil && !identity.IsPendingReference(id) && !identity.IsSessionID(id) {
			return []string{id}, nil
		}
		return nil, platforms.ErrNotFound
	}
	if reader, ok := adapter.(platforms.SessionDescendantReader); ok {
		return reader.SessionDescendants(ctx, id)
	}
	return nil, platforms.ErrUnsupported
}

func (s *Server) sessionTitle(ctx context.Context, id string) string {
	if row, err := s.sessionMetadata(ctx, "", id); err == nil && row != nil {
		return row.Title
	}
	return ""
}
