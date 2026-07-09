package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/loops"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
	"github.com/NoUseFreak/ocman/internal/term"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

//go:embed static/*
var staticFS embed.FS

const (
	autoArchiveAfter        = 72 * time.Hour
	autoArchiveProjectAfter = 7 * 24 * time.Hour
	autoArchiveInterval     = 24 * time.Hour
	projectsScanInterval    = 5 * time.Minute
)

// Server serves the web UI and API.
type Server struct {
	db                 *db.DB
	stateDB            *state.DB
	addr               string
	registry           *platforms.Registry
	sessions           *sessionsvc.Service
	auth               *Auth
	integrations       *forgeClients
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

	// remoteAccess describes this instance's own remote-access surface
	// (instance ID, whether the gRPC server is listening, its address,
	// and whether TLS is on). Surfaced via GET /api/settings/remote-access
	// for the multi-remote-support feature. Zero value = not listening.
	remoteAccess remoteAccessInfo

	// hostRouter resolves the owning hostsvc.Host for a directory or an
	// explicit remote owner (AD-16). git/worktree/tmux/projects handlers
	// delegate through it instead of calling host helpers directly, so
	// remote support is automatic once remote hosts are registered.
	hostRouter *hostsvc.Router

	// remotes is the hub-side manager of attached remote connections
	// (multi-remote support). Nil for single-host installs. The /api/
	// remotes handlers and machine picker use it.
	remotes *remote.Manager

	// remoteProjectsFn sources remote projects for handleProjects. Nil
	// means "use s.remotes"; tests override it to inject remote-tagged
	// rows without a full remote.Manager.
	remoteProjectsFn func() []db.ProjectStats

	// loopSvcCached is the agent-loops domain service, built lazily on
	// first use (it needs the registry to be fully wired). Guarded by
	// loopSvcOnce. See loop_engine.go.
	loopSvcCached *loops.Service
	loopSvcOnce   sync.Once
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
		db:           database,
		stateDB:      stateDB,
		addr:         addr,
		registry:     registry,
		auth:         auth,
		integrations: newForgeClients(),
		startTime:    time.Now(),
		broadcastHub: newBroadcastHub(),
	}
	s.hostRouter = hostsvc.NewRouter(s.newLocalHost())
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
		SessionCreated: s.refreshProjectsIndexAsync,
	})
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
func (s *Server) refreshProjectsIndexAsync() {
	go func() {
		if err := s.refreshProjectsIndex(); err != nil {
			log.WithError(err).Warn("refreshing projects index after session creation")
		}
	}()
}

// SessionService returns the session mutation service so main.go can
// wire the remote-access gRPC server over the same code path (and the
// same hooks) the HTTP layer uses.
func (s *Server) SessionService() *sessionsvc.Service { return s.sessions }

// newLocalHost builds the in-process hostsvc.Host, wiring the tmux,
// projects, and capability operations that live in this package into the
// dependency-injected local Host (which owns the the git package call
// sites directly). See internal/hostsvc/local.
func (s *Server) newLocalHost() hostsvc.Host {
	return hostlocal.New(hostlocal.Deps{
		LaunchTmux:            tmux.LaunchOpencode,
		LaunchProjectOpencode: launchProjectOpencode,
		DiscoverPort:          opencode.DiscoverOpenCodePortFresh,
		// CreateSession routes worktree-session creation through the shared
		// session-mutation service (same validated path + hooks as REST/MCP).
		// Resolved lazily: s.sessions is assigned after newLocalHost runs.
		CreateSession: func(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return s.sessions.Client("opencode").CreateSession(ctx, req)
		},
		TmuxSessions:     s.hostTmuxSessions,
		Projects:         s.hostProjects,
		Caps:             s.hostCaps,
		TermWindows:      term.Windows,
		TermCreateWindow: term.CreateWindow,
		TermKillWindow:   term.KillWindow,
		TermAttach:       term.AttachLocalPTY,
	})
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
func (s *Server) WithPublicBaseURL(base string) *Server {
	s.publicBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
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

// HostRouter returns the host router so the remote Manager (Phase 4+)
// can register remote hosts as connections come up.
func (s *Server) HostRouter() *hostsvc.Router { return s.router() }

// SetRemoteManager attaches the hub-side remote manager. Must be called
// before Start. Nil for single-host installs.
func (s *Server) SetRemoteManager(m *remote.Manager) { s.remotes = m }

// Start starts the HTTP server. It blocks until the context is cancelled,
// then gracefully shuts down the server.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	return s.StartOnListener(ctx, ln)
}

// StartOnListener starts the HTTP server on an already-bound listener. It
// blocks until the context is cancelled, then gracefully shuts down.
// This variant is used by the GUI mode, which picks the port before handing
// the listener here so Wails can point its proxy at the correct address.
func (s *Server) StartOnListener(ctx context.Context, ln net.Listener) error {
	// Seed the cached judge delay so the first permission event has it
	// available without a DB round-trip.
	if s.stateDB != nil {
		if d, err := s.stateDB.GetJudgeDelayMs(); err == nil {
			s.aaSvc().SetJudgeDelayMs(d)
		} else {
			s.aaSvc().SetJudgeDelayMs(state.DefaultJudgeDelayMs)
		}
	} else {
		s.aaSvc().SetJudgeDelayMs(state.DefaultJudgeDelayMs)
	}

	go s.runAutoArchiveLoop(ctx)
	go s.runProjectsIndexLoop(ctx)
	go s.runLLMMetricsLoop(ctx)
	go s.runChildSessionWatcher(ctx)
	go s.runLoopEngine(ctx)
	// Headless auto-approve: subscribe directly to each OpenCode
	// instance's /event SSE stream so permission.asked events drive
	// the judge even when no browser tab is open. Without this, the
	// auto-approve pipeline only fires when a frontend SSE connection
	// happens to be active for some session in the same OpenCode
	// process.
	go s.aaSvc().RunWatcher(ctx)

	// Register observable gauges for the top-line stats (session /
	// message / project counts, lifetime tokens and cost). The
	// callback runs once per OTel collection interval; it's a no-op
	// when telemetry is disabled or the OpenCode DB is absent.
	if reg, err := s.registerStatsMetrics(telemetry.Meter()); err != nil {
		log.WithError(err).Warn("failed to register stats metrics")
	} else if reg != nil {
		defer reg.Unregister()
	}

	mux, err := s.routes()
	if err != nil {
		return err
	}

	// Wrap the mux with the request-timing middleware so every API
	// request emits a "METHOD path -> status (Nms)" debug log line. SSE
	// and the debug-log sink are skipped inside the middleware (see
	// noiseSkip) to keep the log readable.
	//
	// Layering (outer -> inner): withRequestTiming -> withOTel -> mux.
	// otelhttp sits closest to the mux so its server span wraps just
	// the route handlers; withRequestTiming wraps the whole thing so
	// Server-Timing captures otelhttp's overhead too. otelhttp is a
	// no-op when telemetry is disabled (its global TracerProvider is
	// the SDK noop in that case).
	httpServer := &http.Server{
		Addr:    ln.Addr().String(),
		Handler: withRequestTiming(withOTel(mux)),
	}

	// Sweep orphaned ephemeral terminal-viewer sessions left by an
	// earlier process (e.g. after an air rebuild / crash). They can
	// never belong to a live connection at boot, so this self-heals the
	// old per-viewer session leak. Cheap and safe when tmux is absent.
	term.SweepLegacySessions()

	// Start the server in a goroutine so we can wait for the context.
	errCh := make(chan error, 1)
	go func() {
		// Surface the auth posture in the boot log so operators
		// can tell at a glance whether and how clients are gated.
		authMode := "disabled"
		if s.auth != nil {
			if s.auth.TrustsLocalhost() {
				authMode = "password (localhost exempt)"
			} else {
				authMode = "password (all clients)"
			}
		}
		log.WithFields(log.Fields{
			"addr": ln.Addr().String(),
			"auth": authMode,
		}).Info("ocman server started")
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for context cancellation (signal) or server error.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// autoArchiveTickFn is the per-tick body of runAutoArchiveLoop, lifted
// to a package-level variable so tests can inject a panicking
// implementation (FR-11) and assert the loop survives.
var autoArchiveTickFn = func(s *Server) {
	s.autoArchiveInactiveSessions()
	s.autoArchiveInactiveProjects()
}

func (s *Server) runAutoArchiveLoop(ctx context.Context) {
	runWithRecover("auto-archive", func() { autoArchiveTickFn(s) })

	ticker := time.NewTicker(autoArchiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("auto-archive", func() { autoArchiveTickFn(s) })
		}
	}
}

func (s *Server) autoArchiveInactiveSessions() {
	cutoff := time.Now().Add(-autoArchiveAfter).UnixMilli()
	// Each tick is its own root span so the trace tree corresponds
	// to one independent auto-archive pass. Background work doesn't
	// belong under any HTTP request.
	ctx, span := telemetry.Tracer().Start(context.Background(), "ocman.auto_archive.tick")
	defer span.End()

	if autoArchiveRuns != nil {
		autoArchiveRuns.Add(ctx, 1)
	}

	archivedCount := 0

	for _, adapter := range s.registry.Platforms() {
		if !adapter.Available(ctx) {
			continue
		}
		sessions, err := adapter.SessionsInactiveBefore(ctx, cutoff)
		if err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Error("listing inactive sessions for auto-archive")
			continue
		}
		for _, session := range sessions {
			if err := s.stateDB.ArchiveSession(string(adapter.ID()), session.ID, session.TimeUpdated); err != nil {
				span.RecordError(err)
				log.WithFields(log.Fields{
					"platform":  adapter.ID(),
					"sessionID": session.ID,
					"error":     err,
				}).Error("auto-archiving inactive session")
				continue
			}
			archivedCount++
			if autoArchiveSessions != nil {
				autoArchiveSessions.Add(ctx, 1, metric.WithAttributes(
					attribute.String("platform", string(adapter.ID())),
				))
			}
		}
	}

	span.SetAttributes(
		attribute.Int64("ocman.archived_count", int64(archivedCount)),
		attribute.Int64("ocman.cutoff_ms", cutoff),
	)

	log.WithFields(log.Fields{
		"cutoff":   cutoff,
		"archived": archivedCount,
	}).Info("auto-archive pass completed")
}

// autoArchiveInactiveProjects archives local projects whose most recent
// session activity is older than autoArchiveProjectAfter. Archive state
// is keyed by folded project root; already-archived roots are skipped.
// A project auto-unarchives later (in applyProjectArchiveState) once it
// sees fresh activity, so this is safe to re-run.
func (s *Server) autoArchiveInactiveProjects() {
	if s.stateDB == nil || s.db == nil {
		return
	}
	cutoff := time.Now().Add(-autoArchiveProjectAfter).UnixMilli()
	ctx, span := telemetry.Tracer().Start(context.Background(), "ocman.auto_archive_projects.tick")
	defer span.End()

	projects, err := s.router().Local().Projects(ctx)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing projects for auto-archive")
		return
	}

	archived, err := s.stateDB.ArchivedProjects()
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing archived projects for auto-archive")
		return
	}

	// Newest activity per folded root.
	newest := map[string]int64{}
	for _, p := range projects {
		root := projectRootForDirectory(p.Directory)
		if p.LastUsed > newest[root] {
			newest[root] = p.LastUsed
		}
	}

	archivedCount := 0
	for root, last := range newest {
		if last >= cutoff {
			continue
		}
		if _, ok := archived[root]; ok {
			continue
		}
		if err := s.stateDB.ArchiveProject(root); err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"projectRoot": root, "error": err}).
				Error("auto-archiving inactive project")
			continue
		}
		archivedCount++
	}

	span.SetAttributes(
		attribute.Int64("ocman.archived_count", int64(archivedCount)),
		attribute.Int64("ocman.cutoff_ms", cutoff),
	)
	log.WithFields(log.Fields{
		"cutoff":   cutoff,
		"archived": archivedCount,
	}).Info("project auto-archive pass completed")
}

// requireGET wraps a handler to only allow GET requests.
func requireGET(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// requirePOST wraps a handler to only allow POST requests.
func requirePOST(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// get and post compose the method guard with requireAuth. They're
// method-valued so handlers referring to s.auth can see it. When auth
// is disabled, requireAuth is a pass-through.
func (s *Server) get(h http.HandlerFunc) http.HandlerFunc {
	return requireGET(s.requireAuth(h))
}

func (s *Server) post(h http.HandlerFunc) http.HandlerFunc {
	return requirePOST(s.requireAuth(h))
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// serverError logs the real error and returns a generic message to the client.
func serverError(w http.ResponseWriter, msg string, err error) {
	log.WithError(err).Error(msg)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
