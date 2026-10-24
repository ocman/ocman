package server

import (
	"context"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionlifecycle"
)

// Stateless orchestration: callbacks resolve the current owner, adapter and
// options on every call, including options applied after server construction.
func (s *Server) sessionLifecycleModule() *sessionlifecycle.Module {
	return sessionlifecycle.NewModule(sessionlifecycle.Deps{
		Sessions:       func() sessionlifecycle.Sessions { return s.sessions },
		Owner:          s.resolveOwner,
		CreatePlatform: s.createPlatform,
		PermissionDefaults: func(ctx context.Context, dir, owner string) ([]platforms.PermissionRule, error) {
			defaults, err := s.getProjectDefaults(ctx, dir, owner)
			if err != nil || defaults == nil {
				return nil, err
			}
			return defaults.permissionRules()
		},
		ProjectDefaultModel: s.projectDefaultModel,
		Platforms: func() []platforms.Platform {
			if s.registry == nil {
				return nil
			}
			return s.registry.Platforms()
		},
		SendNow:   s.sendNow,
		Broadcast: s.broadcastGlobalEvent,
		WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
			s.withSessionAdapter(w, r, fn)
		},
		Privileged:        s.isPrivilegedRequest,
		Router:            s.router,
		UploadReadTimeout: func() time.Duration { return uploadReadTimeout },
	})
}
