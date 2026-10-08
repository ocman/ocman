package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocmaint"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
)

const (
	autoArchiveInterval  = 24 * time.Hour
	projectsScanInterval = 5 * time.Minute
)

// Server serves the web UI and API.
type Server struct {
	db                 *db.DB
	stateDB            *state.DB
	addr               string
	registry           *platforms.Registry
	sessions           *sessionsvc.Service
	projectModels      projectModelCache
	retryNext          sync.Map // session → latest retry next attempt; 429 reset fallback
	fallAborted        sync.Map // fallKey → provider whose parked retry ocman aborted
	fallNotices        sync.Map // fallKey → fallNotice
	auth               *Auth
	integrations       *forgeClients
	previewAuth        previewAuthState
	startTime          time.Time
	projects           projectsIndexState
	autoApproveDefault bool
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

	// fileKey signs the opaque tokens behind GET /api/file/{token}, the
	// proxy that lets the browser view assets an agent generated on
	// disk (see handlers_file.go). Loaded once, on first use.
	fileKey     []byte
	fileKeyErr  error
	fileKeyOnce sync.Once

	// aaSvcCached is the auto-approve domain service
	// (internal/autoapprove), built lazily on first use via aaSvc() so
	// the dependency fields are wired by then. See autoapprove_engine.go.
	aaSvcCached *autoapprove.Service
	aaOnce      sync.Once

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
	remoteAccess remoteAccessInfo

	// hostRouter resolves the owning hostsvc.Host for a directory or an
	// explicit remote owner (AD-16). git/worktree/tmux/projects handlers
	// delegate through it instead of calling host helpers directly, so
	// remote support is automatic once remote hosts are registered.
	hostRouter     *hostsvc.Router
	hostRouterOnce sync.Once

	// remotes is the hub-side manager of attached remote connections
	// (multi-remote support). Nil for single-host installs. The /api/
	// remotes handlers and machine picker use it.
	remotes *remote.Manager

	// remoteProjectsFn sources remote projects for handleProjects. Nil
	// means "use s.remotes"; tests override it to inject remote-tagged
	// rows without a full remote.Manager.
	remoteProjectsFn func() []db.ProjectStats
	inboxSourcesFn   func() []string
	inboxItemsFn     func(context.Context, string) ([]state.InboxItem, error)

	routineSvc      *routines.Service
	pluginMu        sync.Mutex
	pluginRecovered bool
	pluginDiscovery []pluginDiscoveryFailure
	pluginCtx       context.Context
	// webhookCtx outlives requests so pollers for newly created inboxes keep running.
	webhookCtx context.Context
	// webhookKeyMu serializes relay-side inbox mutations (key resets, secret
	// changes) with their local record.
	webhookKeyMu      sync.Mutex
	pluginProcesses   map[string]*plugins.Process
	pluginStderr      map[string]string
	pluginActionsOnce sync.Once
	pluginActions     *plugins.ActionBroker

	// conversation.v1 state: the broker, its inbound worker and the outbound
	// delivery worker. Everything durable lives in state.db: the
	// thread<->session mapping (plugin_conversation) and the reply outbox
	// (plugin_conversation_outbox), so both survive a restart.
	// conversationInFlight only prevents one process from delivering the same
	// outbox row twice; it is guarded by conversationMu and is not state.
	// See plugin_conversation.go and plugin_conversation_outbox.go.
	conversationOnce       sync.Once
	conversationBroker     *plugins.ConversationBroker
	conversationJobs       *worker.Worker[conversationJob]
	conversationDeliveries *worker.Worker[state.PluginConversationDelivery]
	conversationMu         sync.Mutex
	conversationInFlight   map[int64]bool

	// queueSvcCached is the follow-up message queue service (#58), built
	// lazily on first use. Guarded by
	// queueSvcOnce. See queue.go.
	queueSvcCached  *queuesvc.Service
	queueSvcOnce    sync.Once
	queueWorker     *worker.Worker[queueFlush]
	queueWorkerOnce sync.Once

	// runtime is the local Host's ocruntime.Runtime, injected into
	// newLocalHost. Defaults to the native tmux runtime; tests override it
	// (before the host router is built) with a fake so session-mode
	// handlers don't spawn (and leak) real tmux sessions in temp dirs.
	runtime      ocruntime.Runtime
	openCodeAuth ocapi.Auth
	// maint runs OpenCode database maintenance; nil without a DB path.
	// Its gate refuses opencode launches while a job runs.
	maint             *ocmaint.Runner
	logPath           string // resolved log file; "" when stderr only
	version           string // build version (main.version), reported by /api/stats
	startupIssues     []StartupIssue
	subscriptionUsage subscriptionUsageClient
	// Guards the providers against a reopened panel or a repeatedly
	// clicked Refresh; see subscriptionUsageTTL.
	subscriptionUsageCache subscriptionUsageCache
	factory                factoryService
	// ponytail: unblock sessions are rare; expire tokens if this map becomes measurable.
	factoryUnblockTokens sync.Map

	getNewAssistantMessages func(context.Context, int64) ([]db.LLMMessageRow, int64, error)

	// Stats gauge refresher (see metrics_stats.go): statsSnapshot holds the
	// last successful GetStats result for the OTel callback, guarded by
	// statsMu. getStats / statsRefreshEvery are test seams overriding
	// db.GetStats / statsRefreshInterval.
	getStats          func(context.Context) (*db.Stats, error)
	statsRefreshEvery time.Duration
	statsMu           sync.Mutex
	statsSnapshot     *db.Stats

	projectUpstreamsMu      sync.Mutex
	projectUpstreams        map[string]projectUpstreamsCacheEntry
	projectUpstreamsPending map[string]*projectUpstreamsPending
	projectUpstreamsSlots   chan struct{}
	upstreamNow             func() time.Time
}

type factoryService interface {
	Start(context.Context) error
	Close()
	Status(context.Context) factory.Status
	CreateWorkEpic(context.Context, factory.CreateWorkEpicRequest) (factory.WorkEpic, error)
	ListWorkEpics(context.Context) ([]factory.WorkEpic, error)
	GetWorkEpic(context.Context, string) (factory.WorkEpic, error)
	Pour(context.Context, string) ([]factory.Issue, error)
	ListIssues(context.Context, string) ([]factory.Issue, error)
	ListIssueComments(context.Context, string, string) ([]factory.IssueComment, error)
	AddIssueComment(context.Context, string, string, string, string) (factory.IssueComment, error)
	ClaimPlan(context.Context, string, string) (factory.ClaimedPlan, error)
	Materialize(context.Context, string, string) (factory.Materialization, error)
	SubmitProposal(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
	GetProposal(context.Context, string, int) (factory.ProposalRevision, error)
	ListProposals(context.Context, string) ([]factory.ProposalRevision, error)
	DecidePlanGate(context.Context, string, string, factory.PlanGateDecisionRequest) (factory.PlanGate, error)
	GetFormula(context.Context, string, int) (factory.NativeFormulaView, error)
	ListFormulas(context.Context) ([]factory.NativeFormulaView, error)
	ValidateFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	PreviewFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	SaveFormula(context.Context, factory.FormulaSaveRequest) (factory.NativeFormulaView, error)
	GetCapacityPolicy(context.Context) (factory.CapacityPolicy, error)
	SetCapacityPolicy(context.Context, factory.CapacityPolicy) (factory.CapacityPolicy, error)
	MutateGraph(context.Context, factory.GraphMutation) error
	CreateRecoveryGate(context.Context, string, string, string, string, []string) (factory.RecoveryGate, error)
	ResolveRecoveryGate(context.Context, string, string, string) (factory.RecoveryGate, error)
	IsImplementationSession(context.Context, string) (bool, error)
	EscalatePermission(context.Context, string, string, string, string) (factory.AuthorityEscalationGate, bool, error)
	ResolveAuthorityEscalationGate(context.Context, string, string) (factory.AuthorityEscalationGate, error)
	CompleteAttempt(context.Context, string, string, string, string) error
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

// remoteAccessInfo holds this instance's own remote-access surface for
// display on its Settings page. It is populated from the instance
// identity (state.db) plus the -remote-listen / TLS flags. The token
// itself is never stored here; it is fetched on demand from state.db
// for the explicit reveal action.
type remoteAccessInfo struct {
	instanceID string
	listening  bool
	listenAddr string
	tls        bool
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
		db:                database,
		stateDB:           stateDB,
		addr:              addr,
		registry:          registry,
		auth:              auth,
		integrations:      newForgeClients(),
		subscriptionUsage: newSubscriptionUsageClient(),
		startTime:         time.Now(),
		broadcastHub:      newBroadcastHub(),
		activity:          newClientActivityPolicy(time.Now),

		runtime: ocruntime.NewNativeRuntime(),
	}
	// The host router is built lazily (see router()) so tests can override
	// s.runtime after New before the local Host is constructed.
	// registryRef (not the registry itself) so the service follows a
	// swapped s.registry — tests replace it after construction.
	s.sessions = sessionsvc.New(registryRef{s}, sessionsvc.Hooks{
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
		PermissionRulesChanged: s.applyRulesToPending,
		SessionCreated: func(info sessionsvc.CreatedSession) {
			s.broadcastSessionCreated(info)
			s.refreshProjectsIndexAsync()
		},
		ProjectModels: s.projectModelList,
		CooldownTimes: s.cooldownTimes,
	})
	factorySvc := factory.NewNativeWithExecution(stateDB, factoryProjectResolver{server: s}, factoryPlanningLauncher{server: s}, factoryImplementationLauncher{server: s})
	s.factory = factorySvc
	return s
}

// WithOpenCodeAuth configures managed launch, probes, judge, and watcher
// traffic. The credential remains backend-only.
func (s *Server) WithOpenCodeAuth(auth ocapi.Auth) *Server {
	s.openCodeAuth = auth
	s.runtime = ocruntime.NewNativeRuntimeWithAuth(auth)
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
	if s.db == nil && s.projects.fetch == nil {
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

func (s *Server) RoutineService() *routines.Service { return s.routineSvc }

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
	s.remoteAccess = remoteAccessInfo{
		instanceID: instanceID,
		listening:  listening,
		listenAddr: listenAddr,
		tls:        tls,
	}
	return s
}

// RemoteServerHost returns the in-process local hostsvc.Host. main.go
// uses it to build the remote-access gRPC server over the same registry
// and host the HTTP layer serves (AD-3 — one code path).
func (s *Server) RemoteServerHost() hostsvc.Host { return s.router().Local() }

// Registry returns the platform registry so main.go can wire the
// remote-access gRPC server over the same adapters.
func (s *Server) Registry() *platforms.Registry { return s.registry }

func (s *Server) StateDB() *state.DB { return s.stateDB }

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

// requireMethod wraps a handler to only allow the given HTTP method.
func requireMethod(method string) func(http.HandlerFunc) http.HandlerFunc {
	return func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h(w, r)
		}
	}
}

// requireGET wraps a handler to only allow GET requests.
func requireGET(h http.HandlerFunc) http.HandlerFunc { return requireMethod(http.MethodGet)(h) }

// requirePOST wraps a handler to only allow POST requests.
func requirePOST(h http.HandlerFunc) http.HandlerFunc { return requireMethod(http.MethodPost)(h) }

// get and post compose the method guard with requireAuth. They're
// method-valued so handlers referring to s.auth can see it. When auth
// is disabled, requireAuth is a pass-through.
func (s *Server) get(h http.HandlerFunc) http.HandlerFunc {
	return requireGET(s.requireAuth(h))
}

func (s *Server) post(h http.HandlerFunc) http.HandlerFunc {
	return requirePOST(s.requireAuth(h))
}
