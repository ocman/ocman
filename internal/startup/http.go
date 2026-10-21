package startup

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type httpKey struct{}

// OwnsListener reports whether Run owns the listener across application startup
// failures. Only standalone application servers may close it themselves.
func OwnsListener(ctx context.Context) bool {
	_, ok := ctx.Value(httpKey{}).(*gateway)
	return ok
}

type gateway struct {
	mu       sync.RWMutex
	app      http.Handler
	progress *Progress
	assets   http.Handler
	index    []byte
	server   *http.Server
	done     chan error
}

// Run serves a read-only startup UI immediately, then hands the same HTTP
// server to the application. compose still owns database and worker lifetimes.
// Failure stays visible until shutdown, without exposing application routes.
func Run(ctx context.Context, ln net.Listener, assets fs.FS, compose func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx, p := New(ctx)
	g := &gateway{progress: p, assets: http.FileServer(http.FS(assets)), done: make(chan error, 1)}
	g.index, _ = fs.ReadFile(assets, "index.html")
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	ctx = context.WithValue(ctx, httpKey{}, g)
	go func() {
		err := g.server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		g.done <- err
		cancel()
	}()
	heartbeatDone := make(chan struct{})
	go func() { defer close(heartbeatDone); p.Heartbeat(ctx) }()
	defer func() {
		cancel()
		_ = g.server.Close()
		<-heartbeatDone
	}()
	Report(ctx, "Opening databases", 0, 0)
	err := compose(ctx)
	if err != nil && ctx.Err() == nil && p.Snapshot().State != "ready" {
		log.WithError(err).Error("startup failed; application remains unavailable")
		p.finish("failed", "Startup failed. Check the server logs before restarting.")
		<-ctx.Done()
	}
	_ = g.server.Close()
	serveErr := <-g.done
	if err != nil {
		return err
	}
	return serveErr
}

// ApplicationServer publishes the fully initialized application's guarded
// handler. Called only after migrations and module startup succeed.
func ApplicationServer(ctx context.Context, app *http.Server) *http.Server {
	if g, ok := ctx.Value(httpKey{}).(*gateway); ok {
		g.mu.Lock()
		g.app = app.Handler
		g.progress.finish("ready", "Ready")
		g.mu.Unlock()
		return g.server
	}
	return app
}

// Serve waits for the existing server when Run already accepted the listener;
// standalone application servers retain their normal Serve lifecycle.
func Serve(ctx context.Context, server *http.Server, ln net.Listener) error {
	if _, ok := ctx.Value(httpKey{}).(*gateway); ok {
		<-ctx.Done()
		return http.ErrServerClosed
	}
	return server.Serve(ln)
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/api/startup" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.progress.Snapshot())
		return
	}
	g.mu.RLock()
	app := g.app
	g.mu.RUnlock()
	if app != nil {
		app.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Server is starting", "code": "starting"})
		return
	}
	if len(r.URL.Path) >= 8 && r.URL.Path[:8] == "/assets/" {
		g.assets.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(g.index)
}
