package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/analytics"
	"github.com/NoUseFreak/ocman/internal/previews"

	"github.com/NoUseFreak/ocman/internal/artifacts"
	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/convstream"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factorymod"
	"github.com/NoUseFreak/ocman/internal/filesystem"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/idleprojects"
	"github.com/NoUseFreak/ocman/internal/inbox"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/notifications"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocmaint"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platformsettings"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/projectforge"
	"github.com/NoUseFreak/ocman/internal/queuemod"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/remotesmod"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/sessionrefrecovery"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/sessiontitles"
	"github.com/NoUseFreak/ocman/internal/sharing"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

const (
	autoArchiveInterval  = 24 * time.Hour
	projectsScanInterval = 5 * time.Minute
)

// Server serves the web UI and API.
type Server struct {
	sessionPreparation      *sessionPreparation
	tasks                   lazy[*tasksvc.Manager]
	historyIngest           *conversationingest.Module
	historyAnalytics        *analytics.HistoryAnalytics
	historyOwner            string
	db                      *db.DB
	subscriptionCredentials func(context.Context) (map[string]db.SubscriptionOAuth, error)
	stateDB                 *state.DB
	addr                    string
	registry                *platforms.Registry
	sessions                *sessionsvc.Service
	auth                    *Auth
	integrations            *forgeClients
	startTime               time.Time
	projects                projectsIndexState
	autoApproveDefault      bool
	// publicBaseURL is the externally reachable base URL used to build
	// absolute share links (e.g. "https://ocman.example.com"). Empty
	// means "derive from the incoming request's scheme + Host header",
	// which works out of the box for localhost / dev. Set via
	// WithPublicBaseURL from the OCMAN_PUBLIC_BASE_URL env or
	// -public-base-url flag. Trailing slash is trimmed.
	publicBaseURL string

	// relayURL is the base URL of the share relay used for
	// cross-machine sharing, set by WithRelay from -relay-url, the
	// OCMAN_RELAY_URL env, or the built-in default. Empty means no
	// relay is configured and shares stay local to this machine.
	relayURL string
	// relaySource names which of those inputs supplied relayURL, so the
	// Settings page can show an operator where the value came from.
	relaySource string

	// mcpAddr is the loopback address of the dedicated MCP listener.
	// Empty means "no dedicated listener" — /mcp is then only reachable
	// on the main port, where password auth applies like everywhere
	// else. See startMCPListener. Set via WithMCPAddr (-mcp-addr).
	mcpAddr string
	// mcpListenErr explains why the dedicated MCP listener did not bind
	// ("" when it bound or was disabled). Reported by /api/doctor.
	mcpListenErr string
	// toolPathErr is the toolpath.Ensure failure, reported by /api/doctor.
	toolPathErr error

	// mcpHandlerCached is the shared MCP handler. Both the main mux and
	// the dedicated listener serve the same instance.
	mcpHandlerCached http.Handler
	mcpHandlerOnce   sync.Once

	// filesystemMod owns directory browsing, signed embedded files and
	// repository file reads; built on first use (filesystemModule).
	filesystemMod lazy[*filesystem.Module]

	// autoapproveMod owns the auto-approve pipeline, its routes and its
	// watcher; built lazily on first use. See autoapprove_engine.go.
	autoapproveMod lazy[*autoapprove.Module]

	// broadcastHub fans out events to every connected /api/events
	// client regardless of which session (if any) they're viewing.
	// Used for cross-page signals such as "permission resolved" so
	// in-app prompt toasts can clear instantly rather than waiting for
	// the next /api/sessions/notify poll.
	broadcastHub *broadcastHub
	activity     *clientActivityPolicy

	// remoteAccess describes this instance's own remote-access surface
	// (instance ID, whether the gRPC server is listening, its address,
	// and whether TLS is on). Surfaced via GET /api/settings/remote-access
	// for the multi-remote-support feature. Zero value = not listening.
	remoteAccess remotesmod.Access

	// hostRouter resolves the owning hostsvc.Host for a directory or an
	// explicit remote owner (AD-16). git/worktree/tmux/projects handlers
	// delegate through it instead of calling host helpers directly, so
	// remote support is automatic once remote hosts are registered.
	hostRouter     *hostsvc.Router
	hostRouterOnce sync.Once

	// remotes is the hub-side manager of attached remote connections
	// (multi-remote support). Nil for single-host installs. The remotes
	// module and the machine picker use it.
	remotes *remote.Manager

	// remoteProjectsFn sources remote projects for handleProjects. Nil
	// means "use s.remotes"; tests override it to inject remote-tagged
	// rows without a full remote.Manager.
	remoteProjectsFn func() []db.ProjectStats

	// routineMod is built on first use (routinesModule); tests may set it
	// before then.
	routineMod    lazy[*routines.Module]
	idleProjects  lazy[*idleprojects.Module]
	sessionTitles lazy[*sessiontitles.Module]
	lifetimeMu    sync.Mutex
	lifetimeCtx   context.Context // guarded by lifetimeMu
	// requests joins admitted HTTP handlers before teardown (both listeners).
	requests         requestFence
	pluginsMod       lazy[*pluginsmod.Module]
	webhookMod       lazy[*webhook.Module]
	artifactMod      lazy[*artifacts.Module]
	inboxMod         lazy[*inbox.Module]
	sharingMod       lazy[*sharing.Module]
	busV             lazy[*module.Bus]
	notificationsMod lazy[*notifications.Module]
	queueMod         *queuemod.Module
	queueModOnce     sync.Once

	// runtime is the local Host's ocruntime.Runtime, injected into
	// newLocalHost. Defaults to the native tmux runtime; tests override it
	// (before the host router is built) with a fake so session-mode
	// handlers don't spawn (and leak) real tmux sessions in temp dirs.
	runtime      ocruntime.Runtime
	openCodeAuth ocapi.Auth
	// useOpenCodeService routes v2 to OpenCode's background service.
	useOpenCodeService bool
	// maintMod runs OpenCode database maintenance; nil without a DB path.
	// Its gate refuses opencode launches while a job runs.
	maintMod      *ocmaint.Module
	logPath       string // resolved log file; "" when stderr only
	version       string // build version (main.version), reported by /api/stats
	startupIssues []StartupIssue
	factoryMod    lazy[*factorymod.Module]
	analyticsMod  lazy[*analytics.Module]

	projectForgeMod     lazy[*projectforge.Module]
	previewsMod         lazy[*previews.Module]
	platformSettingsMod lazy[*platformsettings.Module]
	sessionRecoveryMod  lazy[*sessionrefrecovery.Module]
	convStreamMod       lazy[*convstream.Module]
}

type factoryProjectResolver struct{ server *Server }

func (r factoryProjectResolver) ResolveLocalProject(ctx context.Context, path string) (string, error) {
	owner := r.server.router().ForDir(path)
	if owner.RemoteID() != "local" {
		return "", errors.New("factory only supports local projects")
	}
	project, err := owner.ProjectUpstreams(ctx, path)
	if err != nil {
		return "", err
	}
	return project.RepoRoot, nil
}

// New creates a new server. The registry may be nil, in which case a
// new empty registry is created. Callers that want to pre-register
// platform adapters should pass one built with platforms.NewRegistry().
// Pass a non-nil auth to require a password for non-localhost clients;
// pass nil to leave the server open (the pre-auth behaviour).
func New(database *db.DB, stateDB *state.DB, addr string, registry *platforms.Registry, auth *Auth) *Server {
	if registry == nil {
		registry = platforms.NewRegistry()
	}
	s := &Server{
		db:           database,
		stateDB:      stateDB,
		addr:         addr,
		registry:     registry,
		auth:         auth,
		integrations: discoverForgeClients(),
		startTime:    time.Now(),
		broadcastHub: newBroadcastHub(),
		activity:     newClientActivityPolicy(time.Now),

		runtime: ocruntime.NewNativeRuntime(),
	}
	registry.SetSessionEventSink(s.sessionEventSink())
	if database != nil && database.V2() {
		s.subscriptionCredentials = database.SubscriptionCredentials
	}
	stateDB.SetEnabledPlatforms(s.localPlatformIDs)
	// The host router is built lazily (see router()) so tests can override
	// s.runtime after New before the local Host is constructed.
	// registryRef (not the registry itself) so the service follows a
	// swapped s.registry — tests replace it after construction.
	s.sessions = sessionsvc.New(registryRef{s}, sessionsvc.Hooks{
		BeginTurn: s.beginConfigTurn,
		// Cancel any in-flight auto-approve judge before a permission
		// reply is forwarded: the user has decided, so we must not race
		// their answer with the AI's verdict, and we must not pay for a
		// judge whose result will be discarded anyway. aaSvc().Cancel
		// is safe to call when no judge is running.
		PermissionReplied: func(sessionID, permissionID string) {
			s.aaSvc().Cancel(sessionID, permissionID)
		},
		PermissionReplySucceeded: func(ctx context.Context, platform platforms.ID, req platforms.RespondPermissionRequest) {
			s.aaSvc().HandleDirectPermissionReply(ctx, req.SessionID, req.PermissionID, req.Reply)
			s.resolvePermissionInbox(ctx, string(platform), req.SessionID, req.PermissionID)
		},
		PermissionRulesChanged: func(ctx context.Context, adapter platforms.Platform, sessionID string) {
			s.autoapproveModule().ApplyRulesToPending(ctx, adapter, sessionID)
		},
		SessionCreated: func(info sessionsvc.CreatedSession) {
			s.broadcastSessionCreated(info)
			s.refreshProjectsIndexAsync()
		},
		FirstPromptAdded: s.firstPromptAdded,
		ProjectModels:    s.projectModelList,
		CooldownTimes:    s.cooldownTimes,
	})
	return s
}

func (s *Server) localPlatformIDs() []string {
	var ids []string
	if s.registry == nil {
		return nil
	}
	for _, p := range s.registry.Platforms() {
		if id := string(p.ID()); !isRemotePlatformID(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// WithOpenCodeAuth configures managed launch, probes, judge, and watcher
// traffic. The credential remains backend-only.
func (s *Server) WithOpenCodeAuth(auth ocapi.Auth) *Server {
	s.openCodeAuth = auth
	s.runtime = ocruntime.NewNativeRuntimeWithAuth(auth)
	return s
}

// WithOpenCodeService lets the local host route to OpenCode v2's own
// background service instead of launching a second server. Only for a
// server reading the database that service writes.
func (s *Server) WithOpenCodeService(enabled bool) *Server {
	s.useOpenCodeService = enabled
	return s
}

// registryRef adapts the server's current registry to
// sessionsvc.Registry, resolving s.registry at call time.
type registryRef struct{ s *Server }

func (r registryRef) Get(id platforms.ID) (platforms.Platform, bool) { return r.s.registry.Get(id) }
func (r registryRef) Platforms() []platforms.Platform                { return r.s.registry.Platforms() }
func (r registryRef) PlatformForSession(ctx context.Context, sessionID string) (platforms.Platform, bool) {
	return r.s.registry.PlatformForSession(ctx, sessionID)
}
func (r registryRef) CanonicalSessionIDs() bool { return r.s.registry.CanonicalSessionIDs() }

// refreshProjectsIndexAsync refreshes the projects index off the request
// path: the heavy GetProjects() aggregate (correlated per-directory
// message scans) can take seconds and would otherwise block the create
// response. The new session already exists; the index only feeds cached
// stats, which the background ticker also keeps fresh.
//
// A burst of creations does not fan out into concurrent scans:
// refreshProjectsIndex is singleflighted per owner and each request that
// lands mid-scan is represented by the one dirty follow-up (FR-8).
func (s *Server) refreshProjectsIndexAsync() {
	if !s.HasDemand("projects") {
		s.projects.mu.Lock()
		s.projects.dirty = true
		s.projects.mu.Unlock()
		return
	}
	s.triggerProjectsIndexRefresh()
}

func (s *Server) triggerProjectsIndexRefresh() {
	if !s.hasStoredHistory() && s.projects.fetch == nil {
		return
	}
	st := &s.projects
	st.mu.Lock()
	st.dirty = true
	if st.running {
		st.mu.Unlock()
		return
	}
	st.running = true
	st.done = make(chan struct{})
	done := st.done
	st.mu.Unlock()

	go s.runProjectsRefresh(done)
}

// SessionService returns the session mutation service so main.go can
// wire the remote-access gRPC server over the same code path (and the
// same hooks) the HTTP layer uses.
func (s *Server) SessionService() *sessionsvc.Service { return s.sessions }

// RoutineService is the webhook dispatcher for main.go's remote gRPC
// server, which is wired before Start. Untyped nil without a state DB: a
// typed nil passes the poller's nil check and panics on the first delivery.
func (s *Server) RoutineService() webhook.RoutineDispatcher {
	if svc := s.routineService(); svc != nil {
		return svc
	}
	return nil
}

// localWebhookInboxes is this machine's webhook inbox implementation, which
// the composition root injects into the remote manager.
func (s *Server) localWebhookInboxes() webhook.LocalInboxes {
	return webhook.LocalInboxes{Store: s.stateDB, Routines: s.RoutineService()}
}

// WithAutoApproveDefault sets the server-wide default for auto-approve.
// When true, sessions that have no per-session override start with
// auto-approve enabled. Must be called before Start.
func (s *Server) WithAutoApproveDefault(enabled bool) *Server {
	s.autoApproveDefault = enabled
	return s
}

// WithPublicBaseURL sets the externally reachable base URL used to build
// absolute share links. Empty leaves the "derive from request Host"
// behaviour in place. The trailing slash is trimmed so callers can
// concatenate paths directly. Must be called before Start.
//
// An https:// base URL also marks auth cookies Secure: it's the
// operator telling us TLS is terminated in front of ocman, which
// r.TLS can't see.
func (s *Server) WithPublicBaseURL(base string) *Server {
	s.publicBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
	s.auth.setSecureCookies(strings.HasPrefix(strings.ToLower(s.publicBaseURL), "https://"))
	return s
}

// WithRelay records the share relay used for cross-machine sharing and
// which input supplied it ("flag", "env", or "builtin"). An empty URL
// leaves cross-machine sharing disabled, and clears the source so the
// Settings page never shows a provenance without a value. Must be called
// before Start.
func (s *Server) WithRelay(relayURL, source string) *Server {
	s.relayURL = strings.TrimRight(strings.TrimSpace(relayURL), "/")
	s.relaySource = source
	if s.relayURL == "" {
		s.relaySource = ""
	}
	return s
}

// WithRemoteAccess records this instance's own remote-access surface for
// display on its Settings page (multi-remote-support). instanceID comes
// from state.db; listening/listenAddr/tls reflect the -remote-listen and
// TLS flags. Must be called before Start.
func (s *Server) WithRemoteAccess(instanceID, listenAddr string, listening, tls bool) *Server {
	s.remoteAccess = remotesmod.Access{InstanceID: instanceID, ListenAddr: listenAddr, Listening: listening, TLS: tls}
	return s
}

// RemoteServerHost returns the in-process local hostsvc.Host. main.go
// uses it to build the remote-access gRPC server over the same registry
// and host the HTTP layer serves (AD-3 — one code path).
func (s *Server) RemoteServerHost() hostsvc.Host { return s.router().Local() }

// Registry returns the platform registry so main.go can wire the
// remote-access gRPC server over the same adapters.
func (s *Server) Registry() *platforms.Registry { return s.registry }

// HostRouter returns the host router so the remote Manager (Phase 4+)
// can register remote hosts as connections come up.
func (s *Server) HostRouter() *hostsvc.Router { return s.router() }

// SetRemoteManager attaches the hub-side remote manager. Must be called
// before Start. Nil for single-host installs.
func (s *Server) SetRemoteManager(m *remote.Manager) { s.remotes = m }

// HasDemand reports whether a visible client currently leases scope.
func (s *Server) HasDemand(scope string) bool {
	return s.activity == nil || s.activity.HasDemand(scope)
}
