package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/startup"
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
	defer func() {
		cancelWorkers()
		workers.Wait()
		// A shared scan may outlive the project worker's canceled wait.
		s.projects.mu.RLock()
		done, running := s.projects.done, s.projects.running
		s.projects.mu.RUnlock()
		if running {
			<-done
		}
		finalizeModules(s.modules())
	}()
	if !startup.OwnsListener(ctx) {
		defer ln.Close()
	}
	// Build the host router on this goroutine, before any background loop
	// or handler can reach it. router() assigns lazily, and the loops
	// started below race that assignment otherwise.
	s.router()
	startup.Report(ctx, "Loading project index", 0, 0)
	s.loadProjectsIndexCache(context.WithoutCancel(ctx))
	s.lifetimeMu.Lock()
	s.lifetimeCtx = ctx
	s.lifetimeMu.Unlock()
	// Plugins start (discovery, processes) with the other modules below; their
	// processes stop here, after the listeners and launched modules close.
	defer s.stopPluginProcesses()
	// The auto-approve module seeds the judge delay before the other
	// modules start and runs the headless watcher among their workers.
	startup.Report(ctx, "Starting application modules", 0, 0)
	if err := s.startServingServices(ctx, &workers); err != nil {
		return err
	}
	defer s.stopSessionPreparation()

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
	handler := s.withSessionReadiness(mux)
	httpServer := newHTTPServer(ln.Addr().String(), s.requests.track(s.withHostAllowlist(withSecurityHeaders(withRequestTiming(withOTel(handler))))))
	httpServer.BaseContext = func(net.Listener) context.Context { return ctx }

	// The MCP endpoint also gets its own loopback-only listener so local
	// MCP clients work without a cookie, without exposing the tools
	// through a reverse proxy on the main port.
	stopMCP := s.startMCPListener()
	closeModules, err := s.launchServingServices(ctx)
	if err != nil {
		stopMCP()
		return err
	}
	defer closeModules()
	// Both listeners are closed (forcibly past their grace) before admitted
	// handlers are joined, and those finish before module teardown.
	defer func() { stopMCP(); s.requests.close() }()
	httpServer = startup.ApplicationServer(ctx, httpServer)

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
		if err := startup.Serve(ctx, httpServer, ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	})
	// Wait for context cancellation (signal) or server error.
	return awaitHTTP(ctx, httpServer, errCh)
}

func awaitHTTP(ctx context.Context, httpServer *http.Server, errCh <-chan error) error {
	select {
	case err := <-errCh:
		if ctx.Err() != nil {
			return shutdownHTTP(httpServer, shutdownTimeout)
		}
		return err
	case <-ctx.Done():
		log.Info("shutting down server...")
		return shutdownHTTP(httpServer, shutdownTimeout)
	}
}

// shutdownTimeout bounds a graceful shutdown; a variable for tests.
var shutdownTimeout = 5 * time.Second

// shutdownHTTP stops srv gracefully within timeout, then cuts off what
// remains: a stuck request must not turn a reload into a fatal exit.
func shutdownHTTP(srv *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		log.WithField("timeout", timeout).Warn("graceful shutdown timed out; closing remaining connections")
		return srv.Close()
	}
	return err
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
