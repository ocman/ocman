package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// projectDirectory resolves only ingested owner-local project metadata. Missing
// or ambiguous evidence never falls back to the native platform database.
func (s *Server) projectDirectory() func(context.Context, string) (string, error) {
	if s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return nil
	}
	return func(ctx context.Context, id string) (string, error) {
		return s.historyAnalytics.Store.ProjectDirectory(ctx, s.historyOwner, id)
	}
}

// liveCatalog is the directory catalog of the first available local
// adapter answering live, or nil when no instance answers live (the historical
// LiveConnection=false fallback is not a live catalog).
func (s *Server) liveCatalog(ctx context.Context, directory string) *platforms.DirectoryCatalog {
	if s.registry == nil {
		return nil
	}
	for _, p := range s.registry.Platforms() {
		if !isLocalAdapter(p) || !p.Available(ctx) {
			continue
		}
		catalog, err := p.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: directory})
		if err != nil || catalog == nil || !catalog.LiveConnection {
			continue
		}
		return catalog
	}
	return nil
}
