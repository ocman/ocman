package routines

import (
	"context"
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
	r.Local("/api/routines", m.handle)
	r.Local("/api/routines/", m.handle)
}

// Start settles runs a previous process left running before any new tick.
func (m *Module) Start(ctx context.Context) error { return m.svc.Recover(ctx) }

func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		m.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
