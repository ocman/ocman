// Package queuemod owns follow-up delivery and model quota coordination.
package queuemod

import (
	"context"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worker"
	log "github.com/sirupsen/logrus"
)

type Sessions interface {
	SendMessage(context.Context, string, platforms.SendMessageRequest) error
	Fallthrough(context.Context, string, string) (string, time.Time, bool)
	Abort(context.Context, string, platforms.AbortRequest) error
	RecordCooldown(context.Context, string, time.Duration)
}

type Store interface {
	queuesvc.Store
	GetSetting(context.Context, string) (string, bool, error)
	SetSetting(context.Context, string, string) error
	GetProjectSettings(context.Context, string) (state.ProjectSettings, error)
}

type Deps struct {
	Store          Store
	Sessions       func() Sessions
	Adapter        func(context.Context, string, string) (platforms.Platform, bool)
	SendNow        func(context.Context, string, platforms.SendMessageRequest) error
	PrepareSend    func(context.Context, string, platforms.SendMessageRequest) (func(context.Context) error, error)
	Broadcast      func(string, []byte)
	SessionChanged func(platformID, sessionID string)
	Guards         module.Guards
}

type Module struct {
	d                                   Deps
	projectModels                       projectModelCache
	queue                               *queuesvc.Service
	workerOnce                          sync.Once
	worker                              *worker.Worker[queueFlush]
	retryNext, fallAborted, fallNotices sync.Map
	mu                                  sync.Mutex
	ctx                                 context.Context
	stopped                             bool
	idleHandlers                        sync.WaitGroup
}

func NewModule(d Deps) *Module {
	m := &Module{d: d, ctx: context.Background()}
	m.queue = queuesvc.New(d.Store, m, m, m.BroadcastQueueUpdated)
	return m
}

func (m *Module) Name() string               { return "queue" }
func (m *Module) Service() *queuesvc.Service { return m.queue }

func (m *Module) Routes(r module.Router) {
	// The core dispatcher also matches this prefix. Preserve its 405 for
	// queue paths whose method does not match a feature route.
	for _, path := range []string{"/api/session/{id}/queue", "/api/session/{id}/queue/{qmid}", "/api/session/{id}/queue/{qmid}/move"} {
		r.Handle(path, m.d.Guards.Auth(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}))
	}
	r.Handle("GET /api/session/{id}/queue", m.d.Guards.Auth(m.List))
	r.Handle("DELETE /api/session/{id}/queue/{qmid}", m.d.Guards.Auth(m.Delete))
	r.Handle("POST /api/session/{id}/queue/{qmid}/move", m.d.Guards.Auth(m.Move))
	r.Setting("/api/settings/model-fallthrough", m.d.Guards.Auth(m.Settings))
}

func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	return nil
}

// Run joins all admitted idle work before returning to the database owner.
func (m *Module) Run(ctx context.Context) {
	m.runQueueSweep(ctx)
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.idleHandlers.Wait()
	m.mu.Lock()
	w := m.worker
	m.mu.Unlock()
	if w != nil {
		w.Close()
	}
}

func (m *Module) Drain(ctx context.Context) error {
	m.mu.Lock()
	w := m.worker
	m.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.Drain(ctx)
}

func runWithRecover(name string, f func()) {
	defer func() {
		if v := recover(); v != nil {
			log.WithFields(log.Fields{"loop": name, "panic": v, "stack": string(debug.Stack())}).Error("background loop panicked, continuing on next tick")
		}
	}()
	f()
}
