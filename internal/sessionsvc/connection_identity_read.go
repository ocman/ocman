package sessionsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// identityReadAdapter selects only an exact registration. The identity gateway
// validates its canonical source membership before returning retained metadata.
// Disabled and removed accounts can prove history without acquiring execution
// admission or consulting another account's live credential state.
func (s *Service) identityReadAdapter(ctx context.Context, scope string) (platforms.Platform, error) {
	if s.connections != nil && scope != "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		adapter, ok := s.registry.Get(platforms.ID(scope))
		if !ok {
			return nil, platforms.ErrPlatformUnreachable
		}
		return adapter, nil
	}
	return s.pickAdapter(ctx, scope)
}
