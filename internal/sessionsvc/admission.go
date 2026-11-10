package sessionsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Service) admit(ctx context.Context, p platforms.Platform, sessionID string) (func(), error) {
	release, err := s.beginConnection(ctx, p, sessionID)
	if err != nil {
		return nil, err
	}
	if s.hooks.BeginTurn == nil {
		return release, nil
	}
	finish, err := s.hooks.BeginTurn(ctx, p.ID())
	if err != nil {
		release()
		return nil, err
	}
	return func() { finish(); release() }, nil
}

func (s *Service) beginActivity(ctx context.Context, p platforms.Platform, id string) (context.Context, func(), error) {
	if s.hooks.BeginActivity == nil {
		return ctx, func() {}, nil
	}
	return s.hooks.BeginActivity(ctx, p, id)
}

func (s *Service) resolveActivity(ctx context.Context, sessionID, platformID string) (context.Context, platforms.Platform, func(), error) {
	p, err := s.resolve(ctx, sessionID, platformID)
	if err != nil {
		return ctx, nil, nil, err
	}
	ctx, finish, err := s.beginActivity(ctx, p, sessionID)
	return ctx, p, finish, err
}

func (s *Service) resolveTurn(ctx context.Context, sessionID, platformID string) (context.Context, platforms.Platform, func(), error) {
	ctx, p, activityDone, err := s.resolveActivity(ctx, sessionID, platformID)
	if err != nil {
		return ctx, nil, nil, err
	}
	finish, err := s.admit(ctx, p, sessionID)
	if err != nil {
		activityDone()
		return ctx, nil, nil, err
	}
	return ctx, p, func() { finish(); activityDone() }, nil
}
