package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/startup"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
	log "github.com/sirupsen/logrus"
)

// sessionPreparation fences session-state consumers during the one-time legacy
// identity cutover. Normal restarts do not install this fence. HTTP/auth/tasks
// remain available while migration runs; no mutable registry swap is needed.
type sessionPreparation struct {
	work     func(context.Context) error
	ready    atomic.Bool
	prepared atomic.Bool
	readyCh  chan struct{}
	mcpReady chan struct{}
	stopping chan struct{}
	done     chan struct{}
	cancel   context.CancelFunc
	stopOnce sync.Once
}

// WithSessionPreparation must be called before starting listeners or remotes.
// work is replayable and must run with every session-state consumer stopped.
func (s *Server) WithSessionPreparation(work func(context.Context) error) *Server {
	if work != nil {
		s.sessionPreparation = &sessionPreparation{work: work, readyCh: make(chan struct{}),
			mcpReady: make(chan struct{}), stopping: make(chan struct{}), done: make(chan struct{})}
	}
	return s
}

func (s *Server) SessionsReady() bool {
	return s.sessionPreparation == nil || s.sessionPreparation.ready.Load()
}

func (s *Server) AwaitSessionPreparation(ctx context.Context) bool {
	if s.sessionPreparation == nil {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.sessionPreparation.readyCh:
		return ctx.Err() == nil
	}
}

func (s *Server) withSessionReadiness(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.SessionsReady() && !preparationSafeRequest(r) {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("Cache-Control", "no-store")
			httpx.WriteJSONStatus(w, http.StatusServiceUnavailable, map[string]string{
				"error": "Session data is updating; see background tasks", "code": "session_preparing",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func preparationSafeRequest(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") && path != "/mcp" && !strings.HasPrefix(path, "/mcp/") {
		return true // embedded assets and client-side routes
	}
	switch path {
	case "/api/auth/me", "/api/auth/login", "/api/auth/logout":
		return true
	case "/api/capabilities", "/api/system/stats":
		return r.Method == http.MethodGet
	}
	return r.Method == http.MethodGet && (path == "/api/background-tasks" || strings.HasPrefix(path, "/api/background-tasks/"))
}

// Dedicated MCP calls must use the same fence. Once modules have started, their
// launchers may call MCP before general session admission opens.
func (s *Server) withMCPReadiness(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := s.sessionPreparation; p != nil && !p.prepared.Load() {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "Session data is updating", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) startServingServices(ctx context.Context, workers *sync.WaitGroup) error {
	p := s.sessionPreparation
	if p == nil {
		return s.startSessionServices(ctx, workers)
	}
	if err := s.taskManager().Start(ctx); err != nil {
		return err
	}
	ctx, p.cancel = context.WithCancel(ctx)
	s.modules() // materialize lazy dependencies before concurrent startup
	workers.Go(func() { s.runSessionPreparation(ctx, p) })
	return nil
}

func (s *Server) runSessionPreparation(ctx context.Context, p *sessionPreparation) {
	lifetime := ctx
	var workers sync.WaitGroup
	closeFactory := func() {}
	defer func() {
		<-p.stopping // listeners/handlers must be fenced before module teardown
		closeFactory()
		s.stopPluginProcesses()
		p.cancel()
		workers.Wait()
		s.taskManager().Stop()
		close(p.done)
	}()
	err := s.taskManager().Do(ctx, tasksvc.Spec{Kind: "session.preparation", Key: "canonical-v1",
		Title: "Prepare session identities and retained conversations", Scope: "local"}, func(ctx context.Context, reporter *tasksvc.Reporter) error {
		if err := reportPreparation(ctx, reporter, p.work); err != nil {
			return err
		}
		if err := s.startSessionServices(lifetime, &workers); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.mcpReady:
		}
		p.prepared.Store(true)
		var err error
		closeFactory, err = s.launchFactory(lifetime)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		p.ready.Store(true)
		close(p.readyCh)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		if !p.ready.Load() {
			p.prepared.Store(false)
		}
		log.WithError(err).WithField("sessionsReady", p.ready.Load()).Error("session preparation task failed; see background tasks")
	}
}

func (s *Server) launchServingServices(ctx context.Context) (func(), error) {
	if p := s.sessionPreparation; p != nil {
		close(p.mcpReady)
		return s.stopSessionPreparation, nil
	}
	return s.launchFactory(ctx)
}

func (s *Server) stopSessionPreparation() {
	if p := s.sessionPreparation; p != nil && p.cancel != nil {
		p.stopOnce.Do(func() { p.cancel(); close(p.stopping) })
		<-p.done
	}
}

// Reports can originate inside a state.db transaction. Never write task progress
// on that goroutine: the task service uses the same database. Coalesce updates
// and join the reporter only after migration releases its transactions.
func reportPreparation(ctx context.Context, reporter *tasksvc.Reporter, work func(context.Context) error) error {
	updates := make(chan tasksvc.Progress, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for progress := range updates {
			_ = reporter.Report(progress)
		}
	}()
	defer func() { close(updates); <-done }()
	ctx = startup.WithReporter(ctx, func(phase string, completed, total int) {
		progress := tasksvc.Progress{Phase: phase, Counters: map[string]int64{"processed": int64(completed), "phase-total": int64(total)}}
		select {
		case updates <- progress:
		default:
		}
	})
	return work(ctx)
}
