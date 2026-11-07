package server

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectsmod"
	"github.com/ocman/ocman/internal/sessionlifecycle"
)

// The module owns durable draft setup workers. Dependencies still resolve the
// current owner and adapters at call time.
func (s *Server) sessionLifecycleModule() *sessionlifecycle.Module {
	return s.sessionLifecycle.get(func() *sessionlifecycle.Module {
		d := sessionlifecycle.Deps{
			Sessions:       func() sessionlifecycle.Sessions { return s.sessions },
			Owner:          s.resolveOwner,
			CreatePlatform: s.createPlatform,
			PermissionDefaults: func(ctx context.Context, dir, owner string) ([]platforms.PermissionRule, error) {
				defaults, err := projectsmod.LoadDefaults(ctx, s.stateDB, dir, owner)
				if err != nil || defaults == nil {
					return nil, err
				}
				return defaults.PermissionRules()
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
			Enqueue: func(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
				return s.queueSvc().Enqueue(ctx, platform, true, req)
			},
			DispatchQueued: func(ctx context.Context, platform, id string) { s.queueSvc().TryFlush(ctx, platform, id) },
			QueueList:      func(w http.ResponseWriter, r *http.Request) { s.queueModule().ListHeld(w, r) },
			QueueDelete:    func(w http.ResponseWriter, r *http.Request) { s.queueModule().DeleteHeld(w, r) },
			QueueMove:      func(w http.ResponseWriter, r *http.Request) { s.queueModule().Move(w, r) },
		}
		if s.stateDB != nil {
			d.DB = s.stateDB.SQL()
		}
		return sessionlifecycle.NewModule(d)
	})
}
