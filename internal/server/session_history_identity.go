package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (s *Server) historySessionPublicID(ctx context.Context, instance, reference string) (string, error) {
	canonical := s.registry != nil && s.registry.CanonicalSessionIDs()
	if canonical && !identity.IsSessionID(reference) {
		return "", platforms.ErrHistoryUnavailable
	}
	if s.sessionCatalogue == nil && !canonical {
		return reference, nil
	}
	if s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return "", platforms.ErrHistoryUnavailable
	}
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		return "", err
	}
	return owned.HistoryReference(ctx, catalogue, s.historyAnalytics.Store, instance, reference)
}
