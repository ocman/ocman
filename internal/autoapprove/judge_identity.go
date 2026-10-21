package autoapprove

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

// The OpenCode judge owns native execution and cleanup. Only its in-flight
// application notification crosses the canonical identity boundary.
func (s *Service) judgeSessionIdentity(ctx context.Context, native string) (string, error) {
	adapter := s.OpencodeAdapter()
	canonical, ok := adapter.(interface{ CanonicalSessionIDs() bool })
	if !ok || !canonical.CanonicalSessionIDs() {
		return native, nil
	}
	projector, ok := adapter.(platforms.HelperSessionIdentity)
	if !ok {
		return "", platforms.ErrHistoryUnavailable
	}
	id, err := projector.HelperSessionIdentity(ctx, native)
	if err != nil {
		return "", err
	}
	if !identity.IsSessionID(id) {
		return "", platforms.ErrHistoryUnavailable
	}
	return id, nil
}
