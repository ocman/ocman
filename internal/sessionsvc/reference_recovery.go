package sessionsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
)

func (s *Service) RecoverSessionReferences(ctx context.Context, references *owned.ReferenceStore, scope string) (int, error) {
	if s.identities == nil || references == nil {
		return 0, platforms.ErrHistoryUnavailable
	}
	return references.ResolvePending(ctx, s.identities, scope)
}
