package sessionsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Service) admit(ctx context.Context, p platforms.Platform) (func(), error) {
	if s.hooks.BeginTurn == nil {
		return func() {}, nil
	}
	return s.hooks.BeginTurn(ctx, p.ID())
}

func (s *Service) resolveTurn(ctx context.Context, sessionID, platformID string) (platforms.Platform, func(), error) {
	p, err := s.resolve(ctx, sessionID, platformID)
	if err != nil {
		return nil, nil, err
	}
	finish, err := s.admit(ctx, p)
	return p, finish, err
}
