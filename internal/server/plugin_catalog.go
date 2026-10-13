package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// projectDirectory reads this machine's OpenCode database: the action broker
// runs on the executing owner, which the hub reaches with only the opaque
// project ID. Nil without a database, which omits the field.
func (s *Server) projectDirectory() func(context.Context, string) (string, error) {
	if s.db == nil {
		return nil
	}
	return s.db.ProjectDirectory
}

// liveCatalog is the directory catalog of the first available local
// adapter, or nil when its instance does not answer live (the historical
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
			return nil
		}
		return catalog
	}
	return nil
}
