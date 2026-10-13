package autoapprove

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

// ModuleDeps is what the composition root supplies to the auto-approve
// module. The session-scoped permission paths (SSE tee, notify, replies)
// stay server infrastructure and reach the pipeline through Service.
type ModuleDeps struct {
	// Engine wires the pipeline itself, including its Store; the server
	// leaves Store nil without a state DB.
	Engine Deps
	// Store backs the settings and per-session routes. May be nil.
	Store *state.DB
	// Session resolves the adapter owning a request's session, writing the
	// error response itself when there is none.
	Session func(w http.ResponseWriter, r *http.Request, sessionID string) platforms.Platform
	// DefaultEnabled is read per request: the server-wide default.
	DefaultEnabled func() bool
	// ModelOptions lists selectable judge models; nil asks the running
	// OpenCode instances.
	ModelOptions func(context.Context) []string
	Guards       module.Guards
}

// Module owns the auto-approve routes, the judge settings, the judge delay
// seed and the headless watcher's lifetime.
type Module struct {
	ModuleDeps
	svc *Service
}

var (
	_ module.Module  = (*Module)(nil)
	_ module.Starter = (*Module)(nil)
	_ module.Runner  = (*Module)(nil)
)

func NewModule(deps ModuleDeps) *Module {
	if deps.ModelOptions == nil {
		deps.ModelOptions = runningModelOptions
	}
	return &Module{ModuleDeps: deps, svc: NewService(deps.Engine)}
}

func (m *Module) Name() string { return "autoapprove" }

// Service is the pipeline the server's permission paths drive.
func (m *Module) Service() *Service { return m.svc }

// Routes are more specific than the server's /api/session/ dispatcher, so
// the mux sends the session routes here.
func (m *Module) Routes(r module.Router) {
	auth := m.Guards.Auth
	r.Handle("GET /api/session/{id}/auto-approve", auth(m.withSession(m.handleAutoApproveGet)))
	r.Handle("POST /api/session/{id}/auto-approve", auth(m.withSession(m.handleAutoApproveSet)))
	r.Handle("GET /api/session/{id}/approved-permissions", auth(m.withSession(m.handleApprovedPermissions)))
	r.Setting("/api/settings/prompt-sections", auth(m.handlePromptSections))
	r.Setting("/api/settings/judge-delay", auth(m.handleJudgeDelay))
	r.Setting("/api/settings/judge-model", auth(m.handleJudgeModel))
	r.LocalSetting("/api/settings/judge-endpoint", auth(m.handleJudgeEndpoint))
	r.Handle("GET /api/settings/judge-model/options", auth(m.handleJudgeModelOptions))
}

// Start seeds the cached judge delay so the first permission event has it
// without a database round-trip.
func (m *Module) Start(ctx context.Context) error {
	delay := int64(state.DefaultJudgeDelayMs)
	if m.Store != nil {
		if d, err := m.Store.GetJudgeDelayMs(ctx); err == nil {
			delay = d
		}
	}
	m.svc.SetJudgeDelayMs(delay)
	return nil
}

// Run is the headless watcher: it subscribes to every OpenCode instance's
// event stream so permission prompts are judged with no browser open.
func (m *Module) Run(ctx context.Context) { m.svc.RunWatcher(ctx) }

// runningModelOptions lists every "provider/model" OpenCode knows about,
// asking whichever running instance answers first. Any instance will do:
// the provider catalog is a property of the user's OpenCode config, not of
// the directory the instance serves.
func runningModelOptions(ctx context.Context) []string {
	for _, port := range opencode.DiscoverOpenCodePorts() {
		_, models, err := opencode.ProjectCatalog(ctx, "http://127.0.0.1:"+port, "")
		if err == nil && len(models) > 0 {
			return models
		}
	}
	return nil
}
