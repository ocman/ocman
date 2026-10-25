// Package pluginsmod mounts native plugins on the server: discovery and
// process supervision, owner-local management, the authenticated remote
// PluginOperation seam, palette actions, read-only panes, and the
// conversation.v1 broker with its durable reply outbox. The wire protocol,
// supervisor and brokers stay in internal/plugins and the tables in
// internal/state; this package adapts them to the server's session, queue
// and host infrastructure, which it reaches only through Deps.
package pluginsmod

import (
	"context"
	"maps"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
)

// Sessions creates the session a conversation's first message runs in.
type Sessions interface {
	Create(ctx context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	// CreateConfigured creates a session.v1 session with its permission
	// profile's rules applied before the first prompt.
	CreateConfigured(ctx context.Context, platformID string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error)
}

// Remotes forwards a plugin operation to a connected owner.
type Remotes interface {
	PluginOperation(ctx context.Context, owner string, req remote.PluginRequest) (remote.PluginResponse, error)
}

// Deps is what the composition root supplies. Functions are read at call
// time so options applied after the server is built still take effect.
type Deps struct {
	// ReadyTimeout overrides the process handshake budget. Management adds
	// proportional scheduling slack. Zero keeps the production default.
	ReadyTimeout time.Duration
	// Store holds registrations, grants, receipts and the outbox; nil leaves
	// plugins unavailable.
	Store *state.DB
	// Local is this machine's host. Conversation sessions always run here,
	// never on an owner inferred from the directory.
	Local func() hostsvc.Host
	// CreatePlatform resolves a requested platform, else the model
	// reference's, else the owner's default, into its compound ID and base.
	CreatePlatform func(ctx context.Context, host hostsvc.Host, requested, model string) (id, base string, err error)
	// Platform looks up a registered adapter.
	Platform func(id string) (platforms.Platform, bool)
	Sessions Sessions
	// Enqueue holds a message in the session's follow-up queue.
	Enqueue func(ctx context.Context, platform string, forceQueue bool, req platforms.SendMessageRequest) error
	// Adapter resolves the platform that owns a session.
	Adapter func(ctx context.Context, platformID, sessionID string) (platforms.Platform, bool)
	// Remotes is nil while no remote manager exists.
	Remotes func() Remotes
	// PublicURL makes a link a reader elsewhere can open.
	PublicURL  func(path string) string
	SessionURL func(context.Context, string, string) (string, error)
	// ProjectDirectory resolves an OpenCode project ID to its root on this
	// machine for the context.project.path grant; nil without an OpenCode
	// database.
	ProjectDirectory func(ctx context.Context, projectID string) (string, error)
	// LiveCatalog is the directory catalog of a running local instance, or
	// nil when none answers live; nil disables launch-time catalogs.
	LiveCatalog func(ctx context.Context, directory string) *platforms.DirectoryCatalog
}

// Module is the mounted plugin feature. mu is the lifecycle lock: it is
// always taken before durable state, by discovery, management, admission and
// delivery authorization alike.
type Module struct {
	deps    Deps
	store   *state.DB
	plugins *Store
	// workers are the idle replies and outbox kicks, joined by Run.
	workers module.Workers

	mu          sync.Mutex
	recovered   bool
	discovery   []pluginDiscoveryFailure
	ctx         context.Context // processes run under it; nil once stopped
	workerCtx   context.Context
	stopWorkers context.CancelFunc
	stopping    bool
	processes   map[string]*plugins.Process
	stderr      map[string]string
	actionsOnce sync.Once
	actions     *plugins.ActionBroker

	// conversation.v1: the broker, its inbound worker and the outbound
	// delivery worker. Durable state lives in state.db (plugin_conversation
	// and plugin_conversation_outbox).
	conversationOnce       sync.Once
	conversationBroker     *plugins.ConversationBroker
	conversationJobs       *worker.Worker[conversationJob]
	conversationDeliveries *worker.Worker[state.PluginConversationDelivery]
	conversationOutbox     *outbox[state.PluginConversationDelivery]

	// session.v1: the broker, its inbound run worker and the completed-turn
	// delivery worker. The session<->plugin mapping (plugin_session) and the
	// completion outbox (plugin_session_outbox) live in state.db, so both
	// survive a restart.
	sessionOnce       sync.Once
	sessionBroker     *plugins.SessionBroker
	sessionJobs       *worker.Worker[sessionJob]
	sessionDeliveries *worker.Worker[PluginSessionCompletion]
	sessionOutbox     *outbox[PluginSessionCompletion]
}

var (
	_ module.Module     = (*Module)(nil)
	_ module.Starter    = (*Module)(nil)
	_ module.Runner     = (*Module)(nil)
	_ module.Subscriber = (*Module)(nil)
)

func NewModule(deps Deps) *Module {
	m := &Module{deps: deps, store: deps.Store}
	if deps.Store != nil {
		m.plugins = NewStore(deps.Store)
	}
	m.conversationOutbox = m.newConversationOutbox()
	m.sessionOutbox = m.newSessionOutbox()
	return m
}

func (m *Module) Name() string { return "Plugins" }

func (m *Module) Routes(r module.Router) {
	local := r.Guards().Localhost
	r.Handle("GET /api/plugins", m.handlePluginCatalog)
	r.Handle("GET /api/plugins/discovery", m.handlePluginDiscovery)
	r.Handle("POST /api/plugins/rescan", local(m.handlePluginRescan))
	r.Handle("POST /api/plugins/project-catalog", local(m.handlePluginProjectCatalog))
	m.managementRoutes(r)
	r.Handle("GET /api/plugins/actions", m.handlePluginActions)
	r.Handle("POST /api/plugins/actions/invoke", m.handlePluginActionInvoke)
	r.Handle("GET /api/plugins/actions/artifact", m.handlePluginActionArtifact)
	r.Handle("GET /api/plugins/panes", m.handlePluginPanes)
	r.Handle("GET /api/plugins/pages", m.handlePluginPages)
	r.Handle("GET /api/plugins/model-catalog", m.handlePluginModelCatalog)
	r.Handle("POST /api/plugins/pages/call", m.handlePluginPage)
	r.Handle("GET /api/plugins/panes/read", m.handlePluginPaneRead)
	// Profiles are Settings data (normal auth, like the other settings rows):
	// they only take effect when an approved session.v1 plugin names one.
	r.Handle("/api/permission-profiles", m.handlePermissionProfiles)
	r.Handle("/api/permission-profiles/", m.handlePermissionProfiles)
}

// Bind sets the lifetime context plugin processes run under. Start calls
// it; until then (and after Stop) no process is started.
func (m *Module) Bind(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopWorkers != nil {
		m.stopWorkers()
	}
	m.ctx = ctx
	m.workerCtx, m.stopWorkers = context.WithCancel(ctx)
	m.workers.Bind(m.workerCtx)
}

// Start binds ctx and runs discovery, starting approved processes. A
// discovery failure is logged, never fatal: the server still serves.
func (m *Module) Start(ctx context.Context) error {
	m.Bind(ctx)
	if _, err := m.RescanPlugins(ctx); err != nil {
		log.WithError(err).Warn("plugin discovery failed")
	}
	return nil
}

// Run replays conversation replies and session.v1 completions left
// unacknowledged by a disconnect or a crash, is the clock for their bounded
// retries, and reconciles completed turns whose idle edge was missed.
func (m *Module) Run(ctx context.Context) {
	var wg sync.WaitGroup
	if m.store != nil {
		wg.Go(func() { m.conversationOutbox.run(ctx) })
	}
	wg.Go(func() {
		module.Every(ctx, "plugin-conversation-reconcile", outboxReconcileInterval, m.reconcileConversationReplies)
	})
	// Same guarantees for session.v1 completed turns.
	if m.store != nil {
		wg.Go(func() { m.sessionOutbox.run(ctx) })
	}
	wg.Go(func() {
		module.Every(ctx, "plugin-session-reconcile", outboxReconcileInterval, m.ReconcileSessionCompletions)
	})
	wg.Wait()
	m.mu.Lock()
	m.stopping = true
	if m.stopWorkers != nil {
		m.stopWorkers()
	}
	m.mu.Unlock()
	m.workers.Close()
	// Initialize before closing even when no request was seen. Late consumers
	// can only obtain these closed workers, never start a new dispatcher.
	m.sessionOnce.Do(m.buildSessions)
	m.sessionJobs.Close()
	m.sessionDeliveries.Close()
	m.conversationOnce.Do(m.buildConversations)
	m.conversationJobs.Close()
	m.conversationDeliveries.Close()
}

// Stop unbinds the lifetime context and stops every process.
func (m *Module) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = nil
	m.stopping = true
	m.stopPluginProcessesLocked()
}

// Subscribe records a completed reply when a linked session goes idle. The
// read is slow, so it runs on its own goroutine with its own recovery: the
// bus cannot catch a panic there.
func (m *Module) Subscribe(b *module.Bus) {
	b.OnSessionIdle(func(_ context.Context, e module.SessionIdle) {
		m.workers.Go(func(ctx context.Context) {
			module.RunWithRecover("plugin-conversation-reply", func() {
				m.ReplyToConversation(ctx, e.Platform, e.SessionID)
			})
		})
		m.workers.Go(func(ctx context.Context) {
			module.RunWithRecover("plugin-session-completion", func() {
				m.ReplyToSession(ctx, e.Platform, e.SessionID)
			})
		})
	})
}

// Process is the running process for id, or nil.
func (m *Module) Process(id string) *plugins.Process {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processes[id]
}

// Running counts the running processes.
func (m *Module) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.processes)
}

// AdoptProcesses makes m call through from's running processes while keeping
// none of from's other memory: what a host restarted over the same plugin
// processes looks like to durable state. Restart tests use it.
func (m *Module) AdoptProcesses(from *Module) {
	from.mu.Lock()
	processes := maps.Clone(from.processes)
	from.mu.Unlock()
	m.mu.Lock()
	m.processes = processes
	m.mu.Unlock()
}
