package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/integrations"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

//go:embed static/*
var staticFS embed.FS

const (
	autoArchiveAfter     = 72 * time.Hour
	autoArchiveInterval  = 24 * time.Hour
	projectsScanInterval = 5 * time.Minute
)

// Server serves the web UI and API.
type Server struct {
	db                 *db.DB
	stateDB            *state.DB
	addr               string
	registry           *platforms.Registry
	auth               *Auth
	integrations       *integrations.Registry
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
	judge              *PermissionJudge
	// judgeDelayMs is the cached value of the judge delay setting.
	// Loaded at startup and updated whenever the setting is changed via
	// the API. Accessed without a lock — reads/writes are int64 and
	// the worst case is a stale value for one permission event.
	judgeDelayMs int64

	// sseSessions maps sessionID -> the live SSE writer for any
	// currently-connected client. Used to deliver synthetic
	// ocman.permission.* events from non-SSE code paths (REST
	// resurrection of pending permissions, REST permission listing).
	// Values are pointers so callers that capture the sink survive a
	// registry mutation and see the same close() flag.
	sseSessions   map[string]*sseSink
	sseSessionsMu sync.Mutex

	// autoApprove tracks the per-permission state of the auto-approve
	// pipeline for the lifetime of the ocman process. Keyed by
	// "sessionID|permissionID".
	//
	// Combines what used to be two parallel maps:
	//   - in-flight: a non-nil status.cancel means a judge goroutine
	//     is still running, so a second ensureAutoApprove call must
	//     not start another one. cancel is invoked when the user
	//     replies to the prompt (via ocman API or the OpenCode TUI's
	//     permission.replied event) so the judge aborts before its
	//     verdict can race the user's manual answer.
	//   - judged: status.verdict (and status.reasoning for unsafe
	//     verdicts) record the result so re-firing ensureAutoApprove
	//     for the same permissionID (e.g. when the user re-opens the
	//     session and handleSessionPermissions resurrects the prompt
	//     via REST) short-circuits instead of re-running the LLM.
	//
	// The combined map also stores status.judgeStartsAt and
	// status.checking so a freshly-connected SSE sink (the bug case:
	// the headless watcher claimed the permission before any frontend
	// tab was open) can be brought up to date with a synthetic replay
	// of the most recent applicable ocman.permission.* event when the
	// frontend's REST resurrection path calls ensureAutoApprove.
	// Without that replay the prompt UI would stay frozen waiting for
	// an event that already fired.
	//
	// Process-lifetime only: safe verdicts persist via the
	// ApprovedPermission DB row, so they survive restarts naturally.
	// Unsafe verdicts are forgotten on restart, costing at most one
	// re-judge per pending prompt.
	autoApprove   map[string]*autoApproveStatus
	autoApproveMu sync.Mutex

	// safeCommandCache remembers safe Bash-command verdicts per
	// session keyed by md5(metadata["command"]). When the same raw
	// command shows up again in the same session (with a fresh
	// permissionID), backgroundAutoApprove skips the LLM judge and
	// auto-approves immediately using the cached reasoning. Only
	// safe verdicts are cached — unsafe always re-judges so the
	// user gets fresh reasoning and a one-off flag can't permanently
	// block a benign command. In-memory, process-lifetime only.
	//
	// Outer key: sessionID. Inner key: md5 hex of the command.
	// Inner value: the original judge reasoning (rendered into the
	// "cached: <…>" audit row when a hit fires).
	safeCommandCache   map[string]map[string]string
	safeCommandCacheMu sync.Mutex
}

// sseSink wraps an SSE response writer with the synchronisation needed
// to safely emit events from background goroutines.
//
// The server-side auto-approve pipeline emits events long after the
// triggering permission.asked has been processed (after the configured
// delay + judge execution). The originating SSE connection may have
// closed in the meantime; without coordination, writes to the
// underlying http.ResponseWriter would panic the moment Go's
// connection bookkeeping recycles the bufio.Writer.
//
// close() sets closed=true under mu so any concurrent write either
// completes against the live writer or sees closed=true and skips. All
// writes go through write(), which takes the same mutex.
type sseSink struct {
	w      io.Writer
	flush  func()
	mu     sync.Mutex
	closed bool
}

// write emits a single named SSE event. It is a no-op if the sink has
// been closed (the originating connection went away). Safe to call
// concurrently with close() and with other write() calls — they
// serialise on the sink's mutex.
func (s *sseSink) write(eventType string, data []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	writeSSEEvent(s.w, s.flush, eventType, data)
}

// close marks the sink as closed so future write() calls become no-ops.
// Idempotent.
func (s *sseSink) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
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
	return &Server{
		db:                  database,
		stateDB:             stateDB,
		addr:                addr,
		registry:            registry,
		auth:                auth,
		integrations:        integrations.New(),
		startTime:           time.Now(),
		judge:            newPermissionJudge(),
		sseSessions:      make(map[string]*sseSink),
		autoApprove:      make(map[string]*autoApproveStatus),
		safeCommandCache: make(map[string]map[string]string),
	}
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
			s.judgeDelayMs = d
		} else {
			s.judgeDelayMs = state.DefaultJudgeDelayMs
		}
	} else {
		s.judgeDelayMs = state.DefaultJudgeDelayMs
	}

	go s.runAutoArchiveLoop(ctx)
	go s.runProjectsIndexLoop(ctx)
	go s.runLLMMetricsLoop(ctx)
	go s.runChildSessionWatcher(ctx)
	// Headless auto-approve: subscribe directly to each OpenCode
	// instance's /event SSE stream so permission.asked events drive
	// the judge even when no browser tab is open. Without this, the
	// auto-approve pipeline only fires when a frontend SSE connection
	// happens to be active for some session in the same OpenCode
	// process.
	go s.runAutoApproveWatcher(ctx)

	// Register observable gauges for the top-line stats (session /
	// message / project counts, lifetime tokens and cost). The
	// callback runs once per OTel collection interval; it's a no-op
	// when telemetry is disabled or the OpenCode DB is absent.
	if reg, err := s.registerStatsMetrics(telemetry.Meter()); err != nil {
		log.WithError(err).Warn("failed to register stats metrics")
	} else if reg != nil {
		defer reg.Unregister()
	}

	mux := http.NewServeMux()

	// API routes — read-only endpoints enforce GET, mutating endpoints
	// enforce POST. Session-scoped routes (/api/session/{id}/...) are
	// dispatched through a single handler because net/http's ServeMux
	// doesn't support path patterns.
	//
	// s.get / s.post compose method + auth checks so the route table
	// stays readable. Routes that are localhost-only (tmux, debug log,
	// hooks) skip the auth layer because isLoopback is strictly
	// stricter than auth.
	mux.HandleFunc("/api/stats", s.get(s.handleStats))
	mux.HandleFunc("/api/metrics", s.get(s.handleMetrics))
	mux.HandleFunc("/api/projects", s.get(s.handleProjects))
	mux.HandleFunc("/api/system/stats", s.get(s.handleSystemStats))
	mux.HandleFunc("/api/sessions", s.requireAuth(s.handleSessionsRoot)) // GET = list, POST = create
	mux.HandleFunc("/api/sessions/notify", s.get(s.handleSessionsNotify))
	mux.HandleFunc("/api/session/", s.requireAuth(s.dispatchSessionSubpath))
	// Public, UNAUTHENTICATED share endpoints. A valid share token is
	// the only credential: anyone with the unguessable URL can view the
	// conversation read-only, even when password auth is configured.
	mux.HandleFunc("/api/share/", requireGET(s.handleSharePublic))
	mux.HandleFunc("/api/activity", s.get(s.handleActivity))
	mux.HandleFunc("/api/models", s.get(s.handleModels))
	mux.HandleFunc("/api/hourly", s.get(s.handleHourly))
	mux.HandleFunc("/api/hourly-tokens", s.get(s.handleHourlyTokens))
	mux.HandleFunc("/api/capabilities", s.get(s.handleCapabilities))
	mux.HandleFunc("/api/favorites", s.requireAuth(s.handleFavoritesRoot)) // GET = list, POST = add, DELETE = remove
	mux.HandleFunc("/api/whisper/status", s.get(s.handleWhisperStatus))
	mux.HandleFunc("/api/transcribe", s.post(s.handleTranscribe))
	mux.HandleFunc("/api/cost/calc", s.post(s.handleCalcCost))
	mux.HandleFunc("/api/git/diff", s.get(s.handleGitDiff))
	mux.HandleFunc("/api/git/info", s.get(s.handleGitInfo))
	mux.HandleFunc("/api/tmux/clients", requireGET(requireLocalhost(s.handleTmuxClients)))
	mux.HandleFunc("/api/tmux/sessions", requireGET(requireLocalhost(s.handleTmuxSessions)))
	mux.HandleFunc("/api/tmux/switch", requirePOST(requireLocalhost(s.handleTmuxSwitch)))
	mux.HandleFunc("/api/tmux/launch-opencode", requirePOST(requireLocalhost(s.handleTmuxLaunchOpencode)))
	// Live terminal: WebSocket bridge that attaches an in-app xterm.js
	// terminal to an existing tmux target via a PTY. localhost-only —
	// this is a live shell. The WS upgrade is a GET that hijacks the
	// connection, so it is NOT wrapped in requireGET (that wrapper can
	// interfere with the upgrade).
	mux.HandleFunc("/api/term/ws", requireLocalhost(s.handleTermWS))
	// Terminal-window management (list / create / kill the dedicated
	// `ocman-term-*` windows backing the in-app terminal tabs). Method
	// is dispatched inside the handler (GET/POST/DELETE). localhost-only.
	mux.HandleFunc("/api/term/windows", requireLocalhost(s.handleTermWindows))

	// Worktree endpoints. List + default-base-ref are read-only and
	// safe to expose to authenticated clients; create-and-launch
	// runs `git worktree add` and spawns tmux/opencode, so it's
	// localhost-only like the other launch endpoints.
	mux.HandleFunc("/api/worktree/list", s.get(s.handleWorktreeList))
	mux.HandleFunc("/api/worktree/default-base-ref", s.get(s.handleWorktreeDefaultBaseRef))
	mux.HandleFunc("/api/worktree/create-and-launch", requirePOST(requireLocalhost(s.handleWorktreeCreateAndLaunch)))

	// PR/Issue sidebar endpoints — see spec/pr-issue-sidebar/. Read-only
	// proxies to GitHub / Forgejo, scoped to the project at ?dir=<abs>.
	mux.HandleFunc("/api/project/upstreams", s.get(s.handleProjectUpstreams))
	mux.HandleFunc("/api/project/prs", s.get(s.handleProjectPRs))
	mux.HandleFunc("/api/project/issues", s.get(s.handleProjectIssues))
	mux.HandleFunc("/api/project/forge-user", s.get(s.handleProjectForgeUser))
	// Launch endpoint: spawns tmux/opencode, so localhost-only like
	// the worktree create-and-launch endpoint.
	mux.HandleFunc("/api/project/handle", requirePOST(requireLocalhost(s.handleProjectHandle)))

	// MCP server — localhost-only, enabled by default. Exposes the
	// session-split tools (split_to_session, split_to_worktree, etc.)
	// to AI coding agents via the Model Context Protocol.
	mcpHandler := requireLocalhost(s.buildMCPHandler().ServeHTTP)
	mux.HandleFunc("/mcp", mcpHandler)
	mux.HandleFunc("/mcp/", mcpHandler)

	// Auth endpoints. /me is unauthenticated by design (the SPA needs
	// to learn its auth state before it can show the lockscreen).
	// /login and /logout are also unauthenticated — /login is where
	// you prove yourself; /logout just clears a cookie and is
	// idempotent for an already-anonymous client.
	mux.HandleFunc("/api/auth/me", requireGET(s.handleAuthMe))
	mux.HandleFunc("/api/auth/login", requirePOST(s.handleAuthLogin))
	mux.HandleFunc("/api/auth/logout", requirePOST(s.handleAuthLogout))

	// Integration endpoints. These proxy requests to third-party APIs
	// using server-side credentials discovered at startup.
	mux.HandleFunc("/api/integrations/status", s.get(s.handleIntegrationsStatus))
	mux.HandleFunc("/api/integrations/github/preview", s.get(s.handleGitHubPreview))

	// Settings endpoints — user preferences that must be shared with the
	// backend (e.g. judge prompt sections used by headless auto-approve).
	mux.HandleFunc("/api/settings/prompt-sections", s.requireAuth(s.handlePromptSections))
	mux.HandleFunc("/api/settings/judge-delay", s.requireAuth(s.handleJudgeDelay))
	// Prompt templates for the PR/Issue sidebar's "Handle this" launch
	// action. Stored in state.db's generic `setting` table (schema v12).
	mux.HandleFunc("/api/settings/prompt-templates", s.requireAuth(s.handlePromptTemplates))

	// Best-effort remote-logging sink for the frontend. Localhost-only so
	// it can't be used to flood logs from the network. See
	// handleDebugLog for the JSON shape.
	mux.HandleFunc("/api/debug/log", requirePOST(requireLocalhost(s.handleDebugLog)))

	// Static files with SPA fallback
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to get static subtree: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if the file exists in static
		f, err := staticContent.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	// Wrap the mux with the request-timing middleware so every API
	// request emits a structured "http request" log line. SSE and the
	// debug-log sink are skipped inside the middleware (see noiseSkip)
	// to keep the log readable.
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
	sweepLegacyTermSessions()

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
var autoArchiveTickFn = func(s *Server) { s.autoArchiveInactiveSessions() }

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
