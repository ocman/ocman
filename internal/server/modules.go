package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/NoUseFreak/ocman/internal/artifacts"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factorymod"
	"github.com/NoUseFreak/ocman/internal/inbox"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/sharing"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

// modules lists the feature modules this server mounts. Routes, startup,
// background work and MCP tools iterate it; append a module here to mount it.
func (s *Server) modules() []module.Module {
	mods := []module.Module{s.factoryModule()}
	if m := s.routinesModule(); m != nil {
		mods = append(mods, m)
	}
	if m := s.webhooksModule(); m != nil {
		mods = append(mods, m)
	}
	if m := s.artifactsModule(); m != nil {
		mods = append(mods, m)
	}
	if m := s.inboxModule(); m != nil {
		mods = append(mods, m)
	}
	if m := s.sharingModule(); m != nil {
		mods = append(mods, m)
	}
	return mods
}

// bus delivers server events to every module that subscribes; built on
// first use, after which the module set is fixed.
func (s *Server) bus() *module.Bus {
	s.busOnce.Do(func() {
		s.busV = &module.Bus{}
		for _, m := range s.modules() {
			if sub, ok := m.(module.Subscriber); ok {
				sub.Subscribe(s.busV)
			}
		}
	})
	return s.busV
}

// factoryModule exists without a state DB too: Factory then answers that
// it is unavailable rather than 404.
func (s *Server) factoryModule() *factorymod.Module {
	s.factoryModOnce.Do(func() {
		if s.factoryMod == nil {
			s.factoryMod = s.newFactoryModule(nil)
		}
	})
	return s.factoryMod
}

// newFactoryModule builds Factory over svc, or over the native service
// when svc is nil.
func (s *Server) newFactoryModule(svc factorymod.Service) *factorymod.Module {
	return factorymod.NewModule(factorymod.Deps{
		Store:    s.stateDB,
		Service:  svc,
		Registry: registryRef{s},
		Router:   s.router,
		Sessions: s.sessions,
		Enqueue: func(ctx context.Context, platform string, forceQueue bool, req platforms.SendMessageRequest) error {
			return s.queueSvc().Enqueue(ctx, platform, forceQueue, req)
		},
		ResolveForge:   s.resolveForge,
		ResolveProject: factoryProjectResolver{s}.ResolveLocalProject,
		OpenCodeAuth:   func() ocapi.Auth { return s.openCodeAuth },
		MCPURL:         s.mcpServerURL,
		Guards:         module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
	})
}

func (s *Server) sharingModule() *sharing.Module {
	s.sharingModOnce.Do(func() {
		if s.sharingMod != nil || s.stateDB == nil {
			return
		}
		s.sharingMod = sharing.NewModule(sharing.Deps{
			Store:       s.stateDB,
			Session:     s.resolvePlatformForSession,
			Platform:    func(id platforms.ID) (platforms.Platform, bool) { return s.registry.Get(id) },
			RelayURL:    func() string { return s.relayURL },
			RelaySource: func() string { return s.relaySource },
			Guards:      module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		})
	})
	return s.sharingMod
}

func (s *Server) inboxModule() *inbox.Module {
	s.inboxModOnce.Do(func() {
		if s.inboxMod == nil && s.stateDB != nil {
			s.inboxMod = s.newInboxModule(func() inbox.Remotes {
				if s.remotes == nil {
					return nil // untyped: a nil *remote.Manager would pass the module's nil check
				}
				return s.remotes
			})
		}
	})
	return s.inboxMod
}

func (s *Server) newInboxModule(remotes func() inbox.Remotes) *inbox.Module {
	return inbox.NewModule(inbox.Deps{
		Store:         s.stateDB,
		Remotes:       remotes,
		Platform:      func(id platforms.ID) (platforms.Platform, bool) { return s.registry.Get(id) },
		EnsurePending: s.ensurePendingPermissions,
		SessionTitle: func(ctx context.Context, id string) string {
			if s.db == nil {
				return ""
			}
			if row, err := s.db.GetSession(ctx, id); err == nil {
				return row.Title
			}
			return ""
		},
		Guards:    module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		Broadcast: s.broadcastGlobalEvent,
	})
}

// notifyPermissionInbox and resolvePermissionInbox are the permission
// pipeline's hooks into the Inbox; no-ops without a state DB.
func (s *Server) notifyPermissionInbox(permission state.InboxPermission) {
	if m := s.inboxModule(); m != nil {
		m.NotifyPermission(permission)
	}
}

func (s *Server) resolvePermissionInbox(ctx context.Context, platform, sessionID, permissionID string) {
	if m := s.inboxModule(); m != nil {
		m.ResolvePermission(ctx, platform, sessionID, permissionID)
	}
}

// artifactsModule reads server state (auth, relay, public URL) at call
// time, so options applied after New still take effect.
func (s *Server) artifactsModule() *artifacts.Module {
	s.artifactModOnce.Do(func() {
		if s.artifactMod != nil || s.stateDB == nil {
			return
		}
		s.artifactMod = artifacts.NewModule(artifacts.Deps{
			Store:          s.stateDB,
			ResolveProject: factoryProjectResolver{s}.ResolveLocalProject,
			Projects:       func(ctx context.Context) ([]db.ProjectStats, error) { return s.router().Local().Projects(ctx) },
			Descendants: func(ctx context.Context, id string) ([]string, error) {
				if s.db == nil {
					return []string{id}, nil
				}
				return s.db.GetSessionDescendantIDs(ctx, id)
			},
			Guards:      module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
			FileAuth:    s.requireArtifactFileAuth,
			SignFileURL: s.signArtifactFileURL,
			PublicURL:   s.publicURL,
			RelayURL:    func() string { return s.relayURL },
			Broadcast:   s.broadcastGlobalEvent,
		})
	})
	return s.artifactMod
}

// webhooksModule dispatches to routines, so it exists only beside them.
func (s *Server) webhooksModule() *webhook.Module {
	s.webhookModOnce.Do(func() {
		if svc := s.routineService(); s.webhookMod == nil && svc != nil {
			s.webhookMod = webhook.NewModule(webhook.Deps{
				Store: s.stateDB, Routines: svc, RelayURL: func() string { return s.relayURL },
			})
		}
	})
	return s.webhookMod
}

// routinesModule needs a state DB. Built once, on first use, like router().
func (s *Server) routinesModule() *routines.Module {
	s.routineModOnce.Do(func() {
		if s.routineMod == nil && s.stateDB != nil {
			s.routineMod = routines.NewModule(routines.Deps{
				Store: s.stateDB, Router: s.router(), Sessions: s.sessions, Platforms: s.registry,
			})
		}
	})
	return s.routineMod
}

func (s *Server) routineService() *routines.Service {
	if m := s.routinesModule(); m != nil {
		return m.Service()
	}
	return nil
}

// moduleRouter mounts module routes behind the server's guards.
type moduleRouter struct {
	s   *Server
	mux *http.ServeMux
}

func (r moduleRouter) Handle(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }

func (r moduleRouter) Local(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.requireLocalhost(h))
}

func (r moduleRouter) Setting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.settingsHandler(h))
}

func (r moduleRouter) LocalSetting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.settingsHandler(r.s.requireLocalhost(h)))
}

func (s *Server) mountModules(mux *http.ServeMux) {
	for _, m := range s.modules() {
		m.Routes(moduleRouter{s: s, mux: mux})
	}
}

// startModules runs every Starter, then launches every Runner on workers.
func (s *Server) startModules(ctx context.Context, workers *sync.WaitGroup) error {
	mods := s.modules()
	for _, m := range mods {
		if st, ok := m.(module.Starter); ok {
			if err := st.Start(context.WithoutCancel(ctx)); err != nil {
				return fmt.Errorf("starting %s: %w", m.Name(), err)
			}
		}
	}
	for _, m := range mods {
		if r, ok := m.(module.Runner); ok {
			workers.Go(func() { r.Run(ctx) })
		}
	}
	return nil
}

// launchModules runs every Launcher once the MCP listener is up. The
// returned func closes the launched modules in reverse order; on error the
// ones already launched are closed and the failing one is not.
func launchModules(ctx context.Context, mods []module.Module) (func(), error) {
	var launched []module.Closer
	closeAll := func() {
		for i := len(launched) - 1; i >= 0; i-- {
			launched[i].Close()
		}
	}
	for _, m := range mods {
		l, ok := m.(module.Launcher)
		if !ok {
			continue
		}
		if err := l.Launch(ctx); err != nil {
			closeAll()
			return func() {}, fmt.Errorf("starting %s: %w", m.Name(), err)
		}
		if c, ok := m.(module.Closer); ok {
			launched = append(launched, c)
		}
	}
	return closeAll, nil
}
