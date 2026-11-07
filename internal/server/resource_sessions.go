package server

import (
	"context"

	"github.com/ocman/ocman/internal/sessioncatalog"
)

// resourceSessions lists catalogue rows for a resource owner. The local host
// reports "local", while this machine's catalogue rows use its instance owner.
func (s *Server) resourceSessions(ctx context.Context, owner string, limit, offset int) ([]sessioncatalog.Session, error) {
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		return nil, err
	}
	if owner == "local" {
		owner = s.historyOwner
	}
	return catalogue.List(ctx, owner, limit, offset)
}
