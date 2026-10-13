// Package factorymod mounts Factory on the server: its REST routes, the
// factory and factory_control MCP tools, the planning/implementation session
// launchers, Epic control sessions, and the domain service's lifecycle. The
// domain model and its persistence stay in internal/factory and
// internal/state; this package adapts them to the server's shared session
// and host infrastructure, which it reaches only through Deps.
package factorymod

import (
	"context"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Service is the Factory domain service the module drives. Optional
// capabilities (queue, transfers, pausing, idle wake-ups, ...) are checked
// with type assertions so test fakes implement only what they exercise.
type Service interface {
	Start(context.Context) error
	Close()
	Status(context.Context) factory.Status
	CreateWorkEpic(context.Context, factory.CreateWorkEpicRequest) (factory.WorkEpic, error)
	ListWorkEpics(context.Context) ([]factory.WorkEpic, error)
	GetWorkEpic(context.Context, string) (factory.WorkEpic, error)
	Pour(context.Context, string) ([]factory.Issue, error)
	ListIssues(context.Context, string) ([]factory.Issue, error)
	ListIssueComments(context.Context, string, string) ([]factory.IssueComment, error)
	AddIssueComment(context.Context, string, string, string, string) (factory.IssueComment, error)
	ClaimPlan(context.Context, string, string) (factory.ClaimedPlan, error)
	Materialize(context.Context, string, string) (factory.Materialization, error)
	SubmitProposal(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
	GetProposal(context.Context, string, int) (factory.ProposalRevision, error)
	ListProposals(context.Context, string) ([]factory.ProposalRevision, error)
	DecidePlanGate(context.Context, string, string, factory.PlanGateDecisionRequest) (factory.PlanGate, error)
	GetFormula(context.Context, string, int) (factory.NativeFormulaView, error)
	ListFormulas(context.Context) ([]factory.NativeFormulaView, error)
	ValidateFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	PreviewFormula(context.Context, string, string) (factory.NativeFormulaView, error)
	SaveFormula(context.Context, factory.FormulaSaveRequest) (factory.NativeFormulaView, error)
	GetCapacityPolicy(context.Context) (factory.CapacityPolicy, error)
	SetCapacityPolicy(context.Context, factory.CapacityPolicy) (factory.CapacityPolicy, error)
	MutateGraph(context.Context, factory.GraphMutation) error
	CreateRecoveryGate(context.Context, string, string, string, string, []string) (factory.RecoveryGate, error)
	ResolveRecoveryGate(context.Context, string, string, string) (factory.RecoveryGate, error)
	IsImplementationSession(context.Context, string) (bool, error)
	EscalatePermission(context.Context, string, string, string, string) (factory.AuthorityEscalationGate, bool, error)
	ResolveAuthorityEscalationGate(context.Context, string, string) (factory.AuthorityEscalationGate, error)
	CompleteAttempt(context.Context, string, string, string, string) error
}

// Registry resolves platform adapters at call time.
type Registry interface {
	Get(platforms.ID) (platforms.Platform, bool)
	Platforms() []platforms.Platform
}

// Sessions is the server's shared session mutation path.
type Sessions interface {
	CreateConfigured(context.Context, string, platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error)
	SendMessage(context.Context, string, platforms.SendMessageRequest) error
	Abort(context.Context, string, platforms.AbortRequest) error
	RespondPermission(context.Context, string, platforms.RespondPermissionRequest) error
}

// Deps is what the composition root supplies. Functions are read at call
// time so options applied after the server is built still take effect.
type Deps struct {
	// Store backs Factory; nil leaves it unavailable.
	Store *state.DB
	// Service overrides the native service built over Store (tests).
	Service  Service
	Registry Registry
	Router   func() *hostsvc.Router
	Sessions Sessions
	// Enqueue holds a message in the session's follow-up queue.
	Enqueue        func(ctx context.Context, platform string, forceQueue bool, req platforms.SendMessageRequest) error
	ResolveForge   func(forge.Remote) (forge.Forge, bool)
	ResolveProject func(context.Context, string) (string, error)
	OpenCodeAuth   func() ocapi.Auth
	// MCPURL is the endpoint Factory sessions must have configured.
	MCPURL func() string
	// CreatePlatform resolves a requested (or the default) platform for an
	// owner into its compound ID and base platform.
	CreatePlatform func(ctx context.Context, host hostsvc.Host, requested string) (id, base string, err error)
	Guards         module.Guards
}

// Module is the mounted Factory feature.
type Module struct {
	deps          Deps
	svc           Service
	controlFlight singleflight.Group // one control session launch per Epic at a time
}

var (
	_ module.Module       = (*Module)(nil)
	_ module.Launcher     = (*Module)(nil)
	_ module.Closer       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
	_ module.Subscriber   = (*Module)(nil)
)

type projectResolver func(context.Context, string) (string, error)

func (f projectResolver) ResolveLocalProject(ctx context.Context, path string) (string, error) {
	return f(ctx, path)
}

func NewModule(deps Deps) *Module {
	m := &Module{deps: deps, svc: deps.Service}
	if m.svc == nil {
		m.svc = factory.NewNativeWithExecution(deps.Store, projectResolver(deps.ResolveProject), PlanningLauncher{m}, ImplementationLauncher{m})
	}
	return m
}

// Service is the domain service, for the server's permission pipeline.
func (m *Module) Service() Service { return m.svc }

// PlanningLauncher and ImplementationLauncher run Factory sessions on the
// server's session and host infrastructure.
func (m *Module) PlanningLauncher() PlanningLauncher { return PlanningLauncher{m} }

func (m *Module) ImplementationLauncher() ImplementationLauncher { return ImplementationLauncher{m} }

func (m *Module) Name() string { return "Factory" }

func (m *Module) Routes(r module.Router) {
	auth := m.deps.Guards.Auth
	r.Handle("/api/factory/status", get(auth(m.handleFactoryStatus)))
	r.Handle("/api/factory/queue", get(auth(m.handleFactoryQueue)))
	r.Handle("/api/factory/recovery-gates/", auth(m.handleFactoryRecoveryGate))
	r.Handle("/api/factory/authority-gates/", auth(m.handleFactoryAuthorityGate))
	r.Handle("/api/factory/project-gates/", auth(m.handleFactoryProjectGate))
	r.Handle("/api/factory/configuration", auth(m.handleFactoryConfiguration))
	r.Handle("/api/factory/epics", auth(m.handleFactoryEpics))
	r.Handle("/api/factory/epics/", auth(m.handleFactoryEpic))
	r.Handle("/api/factory/formulas", auth(m.handleFactoryFormulas))
	r.Handle("/api/factory/formulas/", auth(m.handleFactoryFormulas))
}

func get(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// Launch recovers persisted attempts and starts dispatch. It runs after
// the MCP listener is up: dispatch immediately launches sessions whose
// ocman MCP connection is verified before they are prompted.
func (m *Module) Launch(ctx context.Context) error {
	if listener, ok := m.svc.(interface {
		OnAttention(func(context.Context, string, []string))
	}); ok {
		listener.OnAttention(m.AttentionToControl)
	}
	return m.svc.Start(ctx)
}

// Close stops dispatch and joins its workers and verification checks.
func (m *Module) Close() { m.svc.Close() }

// Subscribe wakes dispatch when a tracked implementation session goes idle.
// The wake is a non-blocking signal, so it cannot delay later subscribers.
func (m *Module) Subscribe(b *module.Bus) {
	notifier, ok := m.svc.(interface{ NotifySessionIdle(string, string) })
	if !ok {
		return
	}
	b.OnSessionIdle(func(_ context.Context, e module.SessionIdle) {
		notifier.NotifySessionIdle(e.Platform, e.SessionID)
	})
}

// Tools are factory and factory_control. Control stays a separate tool:
// OpenCode permissions apply per tool, so folding it into factory would
// either prompt for every action or bypass its confirmation card.
func (m *Module) Tools() []mcpserver.ServerTool {
	svc := factoryMCPService{Service: m.svc, proposeControl: m.ProposeControl}
	return append(factoryServerTools(&factoryTools{svc: svc}), factoryControlServerTools(svc)...)
}
