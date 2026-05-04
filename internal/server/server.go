package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/db"
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
	db        *db.DB
	stateDB   *state.DB
	addr      string
	registry  *platforms.Registry
	auth      *Auth
	startTime time.Time
	projects  projectsIndexState
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
		db:        database,
		stateDB:   stateDB,
		addr:      addr,
		registry:  registry,
		auth:      auth,
		startTime: time.Now(),
	}
}

// Start starts the HTTP server. It blocks until the context is cancelled,
// then gracefully shuts down the server.
func (s *Server) Start(ctx context.Context) error {
	go s.runAutoArchiveLoop(ctx)
	go s.runProjectsIndexLoop(ctx)

	// Register observable gauges for the top-line stats (session /
	// message / project counts, lifetime tokens and cost). The
	// callback runs once per OTel collection interval; it's a no-op
	// when telemetry is disabled or the OpenCode DB is absent.
	if reg, err := s.registerStatsMetrics(telemetry.Meter()); err != nil {
		log.WithError(err).Warn("failed to register stats metrics")
	} else if reg != nil {
		defer reg.Unregister()
	}

	// Refresh Claude Code hook registration against the current
	// listen address. Best-effort — see maybeInstallClaudeHooks for
	// the no-op preconditions.
	s.maybeInstallClaudeHooks()

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

	// Worktree endpoints. List + default-base-ref are read-only and
	// safe to expose to authenticated clients; create-and-launch
	// runs `git worktree add` and spawns tmux/opencode, so it's
	// localhost-only like the other launch endpoints.
	mux.HandleFunc("/api/worktree/list", s.get(s.handleWorktreeList))
	mux.HandleFunc("/api/worktree/default-base-ref", s.get(s.handleWorktreeDefaultBaseRef))
	mux.HandleFunc("/api/worktree/create-and-launch", requirePOST(requireLocalhost(s.handleWorktreeCreateAndLaunch)))

	// Auth endpoints. /me is unauthenticated by design (the SPA needs
	// to learn its auth state before it can show the lockscreen).
	// /login and /logout are also unauthenticated — /login is where
	// you prove yourself; /logout just clears a cookie and is
	// idempotent for an already-anonymous client.
	mux.HandleFunc("/api/auth/me", requireGET(s.handleAuthMe))
	mux.HandleFunc("/api/auth/login", requirePOST(s.handleAuthLogin))
	mux.HandleFunc("/api/auth/logout", requirePOST(s.handleAuthLogout))

	// Best-effort remote-logging sink for the frontend. Localhost-only so
	// it can't be used to flood logs from the network. See
	// handleDebugLog for the JSON shape.
	mux.HandleFunc("/api/debug/log", requirePOST(requireLocalhost(s.handleDebugLog)))

	// Claude Code hook sink. The hook installer (auto-run at ocman
	// launch when the `claude` CLI is on PATH) writes a block into
	// ~/.claude/settings.json that POSTs every hook event here.
	// Localhost-only for the same reason as tmux — only a local
	// process can legitimately fire these events.
	mux.HandleFunc("/api/hooks/claude", requirePOST(requireLocalhost(s.handleClaudeHook)))

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
		Addr:    s.addr,
		Handler: withRequestTiming(withOTel(mux)),
	}

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
			"addr": s.addr,
			"auth": authMode,
		}).Info("ocman server started")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

func (s *Server) runAutoArchiveLoop(ctx context.Context) {
	s.autoArchiveInactiveSessions()

	ticker := time.NewTicker(autoArchiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.autoArchiveInactiveSessions()
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
