package sessionsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// LookupSessionIdentities batches legacy-link compatibility at the integration
// boundary. Canonical platform responses need no native-ID projection.
func (s *Service) LookupSessionIdentities(ctx context.Context, platformID string, refs []string) ([]platforms.SessionIdentity, error) {
	if len(refs) < 1 || len(refs) > 500 {
		return nil, validation("identity batch requires 1..500 native IDs")
	}
	if s.identities == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return s.identities.LookupBatch(ctx, platformID, refs)
}

func (s *Service) ResolveLegacyReferences(ctx context.Context, scope string, refs []string) (map[string]platforms.SessionIdentity, error) {
	if s.identities == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return s.identities.ResolveLegacyReferences(ctx, scope, refs)
}
