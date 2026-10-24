package routines

import (
	"context"
	"net/http"
	"runtime/debug"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/module"
)

const tickInterval = 5 * time.Second

// Module mounts routines on the server: the /api/routines routes, the
// scheduler tick, run recovery at startup, and the routines MCP tool.
type Module struct{ svc *Service }

var (
	_ module.Module       = (*Module)(nil)
	_ module.Starter      = (*Module)(nil)
	_ module.Runner       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
)

func NewModule(deps Deps) *Module { return &Module{svc: New(deps)} }

// Service exposes the routine service to the composition root, which hands
// it to webhook pollers as their dispatcher.
func (m *Module) Service() *Service { return m.svc }

func (m *Module) Name() string { return "routines" }

func (m *Module) Routes(r module.Router) {
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /api/routines":              m.handleList,
		"POST /api/routines":             m.handleCreate,
		"GET /api/routines/{id}":         m.handleGet,
		"PUT /api/routines/{id}":         m.handleUpdate,
		"DELETE /api/routines/{id}":      m.handleDelete,
		"GET /api/routines/{id}/stats":   m.handleStats,
		"POST /api/routines/{id}/run":    m.handleRun,
		"GET /api/routines/{id}/history": m.handleHistory,
	} {
		r.Local(pattern, h)
		r.Local(pattern+"/{$}", h) // a trailing slash is accepted too
	}
}

// Start settles runs a previous process left running before any new tick.
// Start recovers persisted runs; recovery completes even if startup is cancelled.
func (m *Module) Start(ctx context.Context) error { return m.svc.Recover(context.WithoutCancel(ctx)) }

func (m *Module) Run(ctx context.Context) {
	module.Every(ctx, "routines", tickInterval, m.tick)
}

// tick keeps one bad routine from killing the scheduler for the process.
func (m *Module) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"panic": r, "stack": string(debug.Stack())}).Error("routines: tick panicked, continuing on next tick")
		}
	}()
	if err := m.svc.Tick(ctx); err != nil {
		log.WithError(err).Warn("routines: tick")
	}
}

func (m *Module) Tools() []mcpserver.ServerTool {
	return routineServerTools(&routineTools{svc: m.svc})
}
