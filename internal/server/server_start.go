package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
	"github.com/NoUseFreak/ocman/internal/term"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

// Start starts the HTTP server. It blocks until the context is cancelled,
// then gracefully shuts down the server and its owned workers.
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
	ctx, cancelWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	// Workers must finish before callers close databases or remove their files.
	defer func() { cancelWorkers(); workers.Wait() }()
	defer ln.Close()
	// Build the host router on this goroutine, before any background loop
	// or handler can reach it. router() assigns lazily, and the loops
	// started below race that assignment otherwise.
	s.router()
	s.loadProjectsIndexCache(context.WithoutCancel(ctx))
	s.pluginMu.Lock()
	s.pluginCtx = ctx
	s.pluginMu.Unlock()
	defer s.stopPluginProcesses()
	if _, err := s.RescanPlugins(ctx); err != nil {
		log.WithError(err).Warn("plugin discovery failed")
	}
	if s.stateDB != nil && s.routineSvc == nil {
		s.routineSvc = routines.New(routines.Deps{
			Store: s.stateDB, Router: s.router(), Sessions: s.sessions, Platforms: s.registry,
		})
	}
	if s.remotes != nil {
		s.remotes.SetWebhookDispatcher(s.routineSvc)
	}
	if s.db != nil {
		if _, ok := s.registry.Get(opencode.PlatformID); ok {
			opencode.StartSessionsRefresher(ctx, s.db, s.HasDemand)
		}
	}
	// Seed the cached judge delay so the first permission event has it
	// available without a DB round-trip.
	if s.stateDB != nil {
		if d, err := s.stateDB.GetJudgeDelayMs(ctx); err == nil {
			s.aaSvc().SetJudgeDelayMs(d)
		} else {
			s.aaSvc().SetJudgeDelayMs(state.DefaultJudgeDelayMs)
		}
	} else {
		s.aaSvc().SetJudgeDelayMs(state.DefaultJudgeDelayMs)
	}

	if s.routineSvc != nil {
		if err := s.routineSvc.Recover(context.WithoutCancel(ctx)); err != nil {
			return fmt.Errorf("recovering routines: %w", err)
		}
	}
	if s.stateDB != nil {
		inboxes, err := s.stateDB.ListWebhookInboxes(context.WithoutCancel(ctx))
		if err != nil {
			return fmt.Errorf("loading webhook inboxes: %w", err)
		}
		s.webhookCtx = ctx
		for _, inbox := range inboxes {
			workers.Go(func() { (&webhook.Poller{Store: s.stateDB, Inbox: inbox, Routines: s.routineSvc}).Run(ctx) })
		}
		workers.Go(func() { s.runWebhookHistoryCleanup(ctx) })
	}

	workers.Go(func() { s.runAutoArchiveLoop(ctx) })
	workers.Go(func() { s.runProjectsIndexLoop(ctx) })
	workers.Go(func() { s.runLLMMetricsLoop(ctx) })
	workers.Go(func() { s.runDatabaseSizeLoop(ctx) })
	workers.Go(func() { s.runQueueSweep(ctx) })
	workers.Go(func() { ocv2.WatchInstalledVersion(ctx) })
	// OpenCode v2 runs one server per machine; keep it online.
	if sup, ok := s.router().Local().(interface{ RunMachineSupervisor(context.Context) }); ok {
		workers.Go(func() { sup.RunMachineSupervisor(ctx) })
	}
	// Replays conversation replies left unacknowledged by a disconnect or a
	// crash, and is the clock for their bounded retries.
	workers.Go(func() { s.runConversationDeliveryPump(ctx) })
	workers.Go(func() { s.runConversationReplyReconciliation(ctx) })
	workers.Go(func() { s.runRoutines(ctx) })
	workers.Go(func() { s.runPermissionInboxReconciliation(ctx) })
	// Headless auto-approve: subscribe directly to each OpenCode
	// instance's /event SSE stream so permission.asked events drive
	// the judge even when no browser tab is open. Without this, the
	// auto-approve pipeline only fires when a frontend SSE connection
	// happens to be active for some session in the same OpenCode
	// process.
	workers.Go(func() { s.aaSvc().RunWatcher(ctx) })

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
	// Layering (outer -> inner): host allowlist -> security headers ->
	// request timing -> OTel -> mux. The allowlist is outermost so a
	// DNS-rebound Host never reaches a route, authenticated or not.
	// otelhttp sits closest to the mux so its server span wraps just
	// the route handlers; withRequestTiming wraps the whole thing so
	// Server-Timing captures otelhttp's overhead too. otelhttp is a
	// no-op when telemetry is disabled (its global TracerProvider is
	// the SDK noop in that case).
	httpServer := newHTTPServer(ln.Addr().String(), s.withHostAllowlist(withSecurityHeaders(withRequestTiming(withOTel(mux)))))

	// The MCP endpoint also gets its own loopback-only listener so local
	// MCP clients work without a cookie, without exposing the tools
	// through a reverse proxy on the main port.
	stopMCP := s.startMCPListener()
	if err := s.factory.Start(ctx); err != nil {
		stopMCP()
		return fmt.Errorf("starting Factory: %w", err)
	}
	defer s.factory.Close()
	defer stopMCP()

	// Sweep orphaned ephemeral terminal-viewer sessions left by an
	// earlier process (e.g. after an air rebuild / crash). They can
	// never belong to a live connection at boot, so this self-heals the
	// old per-viewer session leak. Cheap and safe when tmux is absent.
	term.SweepLegacySessions(ctx)

	// Start the server in a goroutine so we can wait for the context.
	errCh := make(chan error, 1)
	workers.Go(func() {
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
	})
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

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}
