package convstream

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
)

// Deps are read at call time where they are functions.
type Deps struct {
	// WithSessionAdapter resolves the session's owning adapter exactly as
	// the other session routes do, writing the HTTP error itself.
	WithSessionAdapter func(http.ResponseWriter, *http.Request, func(http.ResponseWriter, *http.Request, string, platforms.Platform))
	// Proxy runs the owner's v1 stream for a session (see ProxyFunc).
	Proxy      ProxyFunc
	Descendant func(ctx context.Context, root Key, child string) bool
	Queue      func(ctx context.Context, key Key) ([]convevents.QueuedMessage, error)
	Keepalive  func() time.Duration
	// CaptureDir, when set, records raw source frames (CaptureEnv).
	CaptureDir string
	// Trace wraps the stream handler inside the login guard; may be nil.
	Trace func(http.HandlerFunc) http.HandlerFunc
}

type Module struct {
	d       Deps
	workers module.Workers
	hub     *Hub
	// connectGrace overrides defaultConnectGrace in tests.
	connectGrace time.Duration
}

func NewModule(d Deps) *Module {
	m := &Module{d: d, connectGrace: defaultConnectGrace}
	m.hub = NewHub(Options{Workers: &m.workers, Descendant: d.Descendant, Queue: d.Queue})
	return m
}

func (*Module) Name() string { return "convstream" }

func (m *Module) Hub() *Hub { return m.hub }

func (m *Module) Routes(r module.Router) {
	h := m.HandleStream
	if m.d.Trace != nil {
		h = m.d.Trace(h)
	}
	r.Handle("GET /api/session/{id}/stream", h)
}

func (m *Module) Start(ctx context.Context) error {
	m.workers.Bind(ctx)
	return nil
}

// Run detaches every source at shutdown and joins the hub's workers before
// the server closes its databases.
func (m *Module) Run(ctx context.Context) {
	<-ctx.Done()
	m.Close()
}

// Close ends every stream and joins the workers; it is idempotent.
func (m *Module) Close() {
	m.hub.Close()
	m.workers.Close()
}

func (m *Module) source(key Key, adapter platforms.Platform) Source {
	return &proxyLiveSource{key: key, adapter: adapter, proxy: m.d.Proxy, grace: m.connectGrace, captureDir: m.d.CaptureDir,
		descendant: func(child string) bool { return m.hub.descendant(key, child) }}
}
