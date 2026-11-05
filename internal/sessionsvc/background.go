package sessionsvc

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Service) SetBackgroundLifecycle(ctx context.Context, platformID string, req platforms.SetBackgroundLifecycleRequest) error {
	if req.TaskID == "" || (req.Lifecycle != "task" && req.Lifecycle != "service") {
		return validation("task ID and lifecycle task or service are required")
	}
	ctx, p, finish, err := s.resolveActivity(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	defer finish()
	manager, ok := p.(platforms.BackgroundTaskManager)
	if !ok || !p.Capabilities().BackgroundTaskLifecycle {
		return platforms.ErrUnsupported
	}
	return manager.SetBackgroundLifecycle(ctx, req)
}
