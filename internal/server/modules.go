package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/NoUseFreak/ocman/internal/artifacts"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factorymod"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/inbox"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/notifications"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/projectforge"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/remotesmod"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/sharing"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/term"
	"github.com/NoUseFreak/ocman/internal/termmod"
	"github.com/NoUseFreak/ocman/internal/tmuxmod"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

// modules lists the feature modules this server mounts. Routes, startup,
// background work and MCP tools iterate it; append a module here to mount it.
func (s *Server) modules() []module.Module {
	// Auto-approve first: it seeds the judge delay, which ran before every
	// other startup step. Plugins next: their discovery came after it.
	mods := []module.Module{s.autoapproveModule(), s.pluginsModule(), s.factoryModule(), s.sessionTitlesModule()}
	if m := s.routinesModule(); m != nil {
		mods = append(mods, m)
	}
	if m := s.idleProjectsModule(); m != nil {
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
	mods = append(mods, s.notificationsModule(), s.snoozeModule(), s.platformSettingsModule())
	return append(mods, s.tmuxModule(), s.filesystemModule(), s.projectForgeModule(), s.previewsModule(), s.analyticsModule(), s.termModule(), s.maintenanceModule(), s.gitModule(), s.remotesModule(), s.sessionLifecycleModule(), s.queueModule())
}

// tmuxModule is stateless, so it is rebuilt on each call.
func (s *Server) tmuxModule() *tmuxmod.Module {
	return tmuxmod.NewModule(tmuxmod.Deps{
		Local:        func() hostsvc.Host { return s.router().Local() },
		ResolveOwner: s.resolveOwner,
		Guards:       module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
	})
}

// termModule is stateless, so it is rebuilt on each call.
func (s *Server) termModule() *termmod.Module {
	return termmod.NewModule(termmod.Deps{
		ResolveOwner: s.resolveOwner,
		Privileged:   s.isPrivilegedRequest,
		SweepLegacy:  term.SweepLegacySessions,
	})
}

// remotesModule reads the manager and access at call time, so
// SetRemoteManager and WithRemoteAccess after New still take effect.
func (s *Server) remotesModule() *remotesmod.Module {
	d := remotesmod.Deps{
		Manager: func() remotesmod.Manager {
			if s.remotes == nil {
				return nil // untyped: a nil *remote.Manager would pass the module's nil check
			}
			return s.remotes
		},
		Access: func() remotesmod.Access { return s.remoteAccess },
		Guards: module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
	}
	if s.stateDB != nil {
		d.Identity = s.stateDB // untyped nil otherwise
	}
	return remotesmod.NewModule(d)
}

// ModuleSchemas lists the state.db schemas feature modules own; main passes
// them to state.Open, which migrates them in this order. A module that adds
// a table declares a state.ModuleSchema in its package and appends it here.
func ModuleSchemas() []state.ModuleSchema {
	return []state.ModuleSchema{notifications.Schema}
}

// projectForgeModule exists without a state DB too: templates then serve
// the defaults. Owner, forge clients and platforms are read at call time.
func (s *Server) projectForgeModule() *projectforge.Module {
	return s.projectForgeMod.get(func() *projectforge.Module {
		d := projectforge.Deps{
			Owner:          s.resolveOwner,
			ResolveForge:   s.resolveForge,
			KnowsForgejo:   s.knowsForgejo,
			CreatePlatform: s.createPlatform,
			Registered: func(id string) bool {
				if s.registry == nil {
					return false
				}
				_, ok := s.registry.Get(platforms.ID(id))
				return ok
			},
			Sessions: s.sessions,
			Guards:   module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		}
		if s.stateDB != nil {
			d.Store = s.stateDB // untyped nil otherwise
		}
		return projectforge.NewModule(d)
	})
}

// bus delivers server events to every module that subscribes; built on
// first use, after which the module set is fixed.
func (s *Server) bus() *module.Bus {
	return s.busV.get(func() *module.Bus {
		b := &module.Bus{}
		for _, m := range s.modules() {
			if sub, ok := m.(module.Subscriber); ok {
				sub.Subscribe(b)
			}
		}
		return b
	})
}

// factoryModule exists without a state DB too: Factory then answers that
// it is unavailable rather than 404.
func (s *Server) factoryModule() *factorymod.Module {
	return s.factoryMod.get(func() *factorymod.Module {
		return s.newFactoryModule(nil)
	})
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
		CreatePlatform: s.createPlatform,
		Guards:         module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
	})
}

func (s *Server) sharingModule() *sharing.Module {
	return s.sharingMod.get(func() *sharing.Module {
		if s.stateDB == nil {
			return nil
		}
		return sharing.NewModule(sharing.Deps{
			Store:       s.stateDB,
			Session:     s.resolvePlatformForSession,
			Platform:    func(id platforms.ID) (platforms.Platform, bool) { return s.registry.Get(id) },
			RelayURL:    func() string { return s.relayURL },
			RelaySource: func() string { return s.relaySource },
			Guards:      module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		})
	})
}

func (s *Server) inboxModule() *inbox.Module {
	return s.inboxMod.get(func() *inbox.Module {
		if s.stateDB == nil {
			return nil
		}
		return s.newInboxModule(func() inbox.Remotes {
			if s.remotes == nil {
				return nil // untyped: a nil *remote.Manager would pass the module's nil check
			}
			return s.remotes
		})
	})
}

func (s *Server) newInboxModule(remotes func() inbox.Remotes) *inbox.Module {
	return inbox.NewModule(inbox.Deps{
		Store:    s.stateDB,
		Remotes:  remotes,
		Platform: func(id platforms.ID) (platforms.Platform, bool) { return s.registry.Get(id) },
		EnsurePending: func(adapter platforms.Platform, sessionID string, prompts []platforms.LivePrompt) {
			s.autoapproveModule().EnsurePending(adapter, sessionID, prompts)
		},
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
	return s.artifactMod.get(func() *artifacts.Module {
		if s.stateDB == nil {
			return nil
		}
		return artifacts.NewModule(artifacts.Deps{
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
}

// webhooksModule dispatches to routines, so it exists only beside them.
func (s *Server) webhooksModule() *webhook.Module {
	return s.webhookMod.get(func() *webhook.Module {
		svc := s.routineService()
		if svc == nil {
			return nil
		}
		return webhook.NewModule(webhook.Deps{
			Store: s.stateDB, Routines: svc, RelayURL: func() string { return s.relayURL },
		})
	})
}

// routinesModule needs a state DB. Built once, on first use, like router().
func (s *Server) routinesModule() *routines.Module {
	return s.routineMod.get(func() *routines.Module {
		if s.stateDB == nil {
			return nil
		}
		return routines.NewModule(routines.Deps{
			Store: s.stateDB, Router: s.router(), Sessions: s.sessions, Platforms: s.registry,
		})
	})
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

// Starters get the cancellable server context; one whose initialization must
// outlive a cancelled startup detaches it itself.
func (s *Server) startModules(ctx context.Context, workers *sync.WaitGroup) error {
	return startModules(ctx, s.modules(), workers)
}

func startModules(ctx context.Context, mods []module.Module, workers *sync.WaitGroup) error {
	for _, m := range mods {
		if st, ok := m.(module.Starter); ok {
			if err := st.Start(ctx); err != nil {
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

// pluginsModule exists without a state DB too: plugin routes then answer
// that plugins are unavailable rather than 404.
func (s *Server) pluginsModule() *pluginsmod.Module {
	return s.pluginsMod.get(func() *pluginsmod.Module {
		return pluginsmod.NewModule(pluginsmod.Deps{
			Store:          s.stateDB,
			Local:          func() hostsvc.Host { return s.router().Local() },
			CreatePlatform: s.createPlatform,
			Platform: func(id string) (platforms.Platform, bool) {
				if s.registry == nil {
					return nil, false
				}
				return s.registry.Get(platforms.ID(id))
			},
			Sessions: s.sessions,
			Enqueue: func(ctx context.Context, platform string, forceQueue bool, req platforms.SendMessageRequest) error {
				return s.queueSvc().Enqueue(ctx, platform, forceQueue, req)
			},
			Adapter: s.adapterForSession,
			Remotes: func() pluginsmod.Remotes {
				if s.remotes == nil {
					return nil // untyped: a nil *remote.Manager would pass the module's nil check
				}
				return s.remotes
			},
			PublicURL: s.publicURL,
			Guards:    module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		})
	})
}

// RescanPlugins and SyncPluginProcesses are the plugin lifecycle's entry
// points for callers outside the module.
func (s *Server) RescanPlugins(ctx context.Context) ([]plugins.Discovery, error) {
	return s.pluginsModule().RescanPlugins(ctx)
}

func (s *Server) SyncPluginProcesses(ctx context.Context) error {
	return s.pluginsModule().SyncPluginProcesses(ctx)
}

func (s *Server) stopPluginProcesses() { s.pluginsModule().Stop() }

// RemotePluginOperation runs only owner-local operations after gRPC
// authentication; it is the remote server's plugin handler.
func (s *Server) RemotePluginOperation(ctx context.Context, req remote.PluginRequest) remote.PluginResponse {
	return s.pluginsModule().RemoteOperation(ctx, req)
}

func (s *Server) routePluginOperation(ctx context.Context, owner string, req remote.PluginRequest) remote.PluginResponse {
	return s.pluginsModule().RouteOperation(ctx, owner, req)
}

// replyToConversation is the session.changed recovery for a terminal turn
// whose idle edge never arrived; the idle edge itself reaches the module
// through the bus.
func (s *Server) replyToConversation(ctx context.Context, platformID, sessionID string) {
	s.pluginsModule().ReplyToConversation(ctx, platformID, sessionID)
}

// conversationPromptNeedsUser is fired by the permission pipeline after it
// decided a prompt is the user's, never on the raw ask.
func (s *Server) conversationPromptNeedsUser(platformID, sessionID, kind, requestID string) {
	s.pluginsModule().PromptNeedsUser(platformID, sessionID, kind, requestID)
}
