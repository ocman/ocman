package sessionsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// withProjectDefault returns model unchanged when it is set, otherwise
// the first entry of the session's project model list ("" when the
// project has none, leaving the platform's own pick in place).
// Soft-fails: a lookup error never blocks the prompt.
func (s *Service) withProjectDefault(ctx context.Context, p platforms.Platform, sessionID, model string) string {
	if model != "" || s.hooks.ProjectModels == nil {
		return model
	}
	dir, ok := s.sessionDirs.Load(sessionID)
	if !ok {
		detail, err := p.Session(ctx, sessionID, 1, 0)
		if err != nil || detail == nil || detail.Session == nil || detail.Session.Directory == "" {
			return ""
		}
		dir = detail.Session.Directory
		// ponytail: unbounded per-session cache, one string per prompted
		// session; add eviction if a process ever outlives millions.
		s.sessionDirs.Store(sessionID, dir)
	}
	if models := s.hooks.ProjectModels(ctx, dir.(string)); len(models) > 0 {
		return models[0]
	}
	return ""
}
