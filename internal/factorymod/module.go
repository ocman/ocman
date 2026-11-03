// Package factorymod mounts Factory on the server: its REST routes, the
// factory and factory_control MCP tools, the planning/implementation session
// launchers, Epic control sessions, and the domain service's lifecycle. The
// domain model and its persistence stay in internal/factory and
// internal/state; this package adapts them to the server's shared session
// and host infrastructure, which it reaches only through Deps.
package factorymod

import (
	"context"
	"sync"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"golang.org/x/sync/singleflight"

	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state"
)

// Service is the Factory domain service the module drives. Production uses
// *factory.NativeService; the interface exists so tests can substitute fakes,
// which embed it so unexercised methods panic.
type Service interface {
	Start(context.Context) error
	StopDispatch()
	ResumeDispatch()
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
	Queue(context.Context) ([]factory.DispatchItem, error)
	ResolveProjectRequest(context.Context, string, string, string, bool) (factory.ProjectRequestGate, error)
	RemoveWorkEpicProject(context.Context, string, string) error
	ListRemovedIssues(context.Context, string) ([]factory.Issue, error)
	DeliverAsIs(context.Context, string) ([]string, error)
	CloseEpic(context.Context, string, bool) error
	StopEpic(context.Context, string) error
	SetEpicPaused(context.Context, string, bool) error
	CloseMol(context.Context, string, string) error
	ReopenIssue(context.Context, string, string) error
	SetEpicModels(context.Context, string, model.EpicModels) (model.EpicModels, error)
	TransferIssue(context.Context, string, string, func(context.Context, factory.Issue) (string, error)) (string, error)
	CancelIssueTransfer(context.Context, string, string) error
	SubmitScopePlan(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
	RequestProject(context.Context, string, string, string, string) (factory.ProjectRequestGate, error)
	RequestCheckpointRecovery(context.Context, string, string) (factory.RecoveryGate, error)
	OnAttention(func(context.Context, string, []string))
	NotifySessionIdle(string, string)
	ReconcileAttention(context.Context)
}

var _ Service = (*factory.NativeService)(nil)

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
	// CreatePlatform resolves a requested platform, else the model
	// reference's, else the owner's default, into its compound ID and base.
	CreatePlatform func(ctx context.Context, host hostsvc.Host, requested, model string) (id, base string, err error)
	// Enabled reports the Factory beta setting at call time; nil means on.
	Enabled func(context.Context) bool
}

// Module is the mounted Factory feature.
type Module struct {
	deps          Deps
	svc           Service
	controlFlight singleflight.Group // one control session launch per Epic at a time

	lifeMu                    sync.Mutex // orders Launch, SyncEnabled and Close
	launched, started, closed bool       // started: Start's recovery has run
}

var (
	_ module.Module       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
	_ module.ToolFilter   = (*Module)(nil)
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

// Launch recovers persisted attempts and starts dispatch when Factory is
// enabled. It runs after the MCP listener is up: dispatch immediately
// launches sessions whose ocman MCP connection is verified before they are
// prompted.
func (m *Module) Launch(ctx context.Context) error {
	m.lifeMu.Lock()
	defer m.lifeMu.Unlock()
	m.launched = true
	return m.sync(ctx)
}

// SyncEnabled follows a change of the Factory setting: enabling starts
// dispatch, disabling stops it. Before Launch or after Close it does
// nothing.
func (m *Module) SyncEnabled(ctx context.Context) error {
	m.lifeMu.Lock()
	defer m.lifeMu.Unlock()
	return m.sync(ctx)
}

func (m *Module) sync(ctx context.Context) error {
	if !m.launched || m.closed {
		return nil
	}
	switch enabled := m.enabled(ctx); {
	case !enabled:
		m.svc.StopDispatch()
	case m.started:
		m.svc.ResumeDispatch()
	default:
		m.svc.OnAttention(m.AttentionToControl)
		if err := m.svc.Start(ctx); err != nil {
			return err
		}
		m.started = true
	}
	return nil
}

func (m *Module) enabled(ctx context.Context) bool {
	return m.deps.Enabled == nil || m.deps.Enabled(ctx)
}

// Close stops dispatch and joins its workers and verification checks.
func (m *Module) Close() {
	// Cancel admitted work before waiting for SyncEnabled to join it.
	// The service rejects any concurrent resume after closing.
	m.svc.Close()
	m.lifeMu.Lock()
	m.closed = true
	m.lifeMu.Unlock()
}

// Subscribe wakes dispatch when a tracked implementation session goes idle.
// The wake is a non-blocking signal, so it cannot delay later subscribers.
func (m *Module) Subscribe(b *module.Bus) {
	b.OnSessionIdle(func(_ context.Context, e module.SessionIdle) {
		m.svc.NotifySessionIdle(e.Platform, e.SessionID)
	})
}

// Tools are factory and factory_control. Control stays a separate tool:
// OpenCode permissions apply per tool, so folding it into factory would
// either prompt for every action or bypass its confirmation card.
func (m *Module) Tools() []mcpserver.ServerTool {
	svc := factoryMCPService{Service: m.svc, proposeControl: m.ProposeControl}
	return append(factoryServerTools(&factoryTools{svc: svc}), factoryControlServerTools(svc)...)
}

// FilterTools hides Factory's tools while Factory is disabled.
func (m *Module) FilterTools(ctx context.Context, tools []mcplib.Tool) []mcplib.Tool {
	if m.enabled(ctx) {
		return tools
	}
	own := map[string]bool{}
	for _, tool := range m.Tools() {
		own[tool.Tool.Name] = true
	}
	visible := make([]mcplib.Tool, 0, len(tools))
	for _, tool := range tools {
		if !own[tool.Name] {
			visible = append(visible, tool)
		}
	}
	return visible
}
