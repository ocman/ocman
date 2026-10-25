// Package sessionlifecycle owns conversation creation and direct session actions.
// Agent mutations stay in sessionsvc; owner and transport resolution stay in core.
package sessionlifecycle

import (
	"context"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type Sessions interface {
	KnownPlatform(string) bool
	DirectoryCatalog(context.Context, string, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error)
	CooldownUntil(string) time.Time
	ResolvePlatformID(context.Context, string) (string, error)
	CreateConfigured(context.Context, string, platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error)
	ExecuteCommand(context.Context, string, platforms.ExecuteCommandRequest) error
	RunShell(context.Context, string, platforms.RunShellRequest) error
	Rename(context.Context, string, platforms.RenameSessionRequest) error
	Abort(context.Context, string, platforms.AbortRequest) error
	Revert(context.Context, string, platforms.RevertSessionRequest) error
	Unrevert(context.Context, string, platforms.UnrevertSessionRequest) error
	Compact(context.Context, string, platforms.CompactRequest) error
	Fork(context.Context, string, platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error)
	Move(context.Context, string, platforms.MoveSessionRequest) error
	SetPermissionRules(context.Context, string, platforms.SetPermissionRulesRequest) error
}

// Deps reads mutable server options and adapters at call time. The direct-send
// callback is also used by the queue, without making queueing this module's job.
type Deps struct {
	Sessions            func() Sessions
	Owner               func(http.ResponseWriter, string, string) (hostsvc.Host, bool)
	CreatePlatform      func(context.Context, hostsvc.Host, string, string) (string, string, error)
	PermissionDefaults  func(context.Context, string, string) ([]platforms.PermissionRule, error)
	ProjectDefaultModel func(context.Context, string) string
	Platforms           func() []platforms.Platform
	SendNow             func(context.Context, string, platforms.SendMessageRequest) error
	Broadcast           func(string, []byte)
	WithSessionAdapter  func(http.ResponseWriter, *http.Request, func(http.ResponseWriter, *http.Request, string, platforms.Platform))
	Privileged          func(*http.Request) bool
	Router              func() *hostsvc.Router
	UploadReadTimeout   func() time.Duration
}

type Module struct{ d Deps }

func NewModule(d Deps) *Module { return &Module{d: d} }
func (*Module) Name() string   { return "sessionlifecycle" }

func (m *Module) Routes(r module.Router) {
	r.Local("POST /api/sessions/prepare", m.HandlePrepareSession)
	r.Local("POST /api/sessions/start", m.HandleStartSession)
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"POST /api/session/{id}/attachment", m.HandleSessionAttachment},
		{"POST /api/session/{id}/restart-opencode", m.HandleSessionRestartOpencode},
		{"POST /api/session/{id}/reload-opencode", m.HandleSessionReloadOpencode},
		{"POST /api/session/{id}/command", m.HandleSessionCommand},
		{"POST /api/session/{id}/shell", m.HandleSessionShell},
		{"POST /api/session/{id}/abort", m.HandleSessionAbort},
		{"POST /api/session/{id}/background/{taskID}/stop", m.HandleBackgroundTaskStop},
		{"POST /api/session/{id}/revert", m.HandleSessionRevert},
		{"POST /api/session/{id}/unrevert", m.HandleSessionUnrevert},
		{"POST /api/session/{id}/compact", m.HandleSessionCompact},
		{"POST /api/session/{id}/fork", m.HandleSessionFork},
		{"POST /api/session/{id}/move", m.HandleSessionMove},
		{"PATCH /api/session/{id}", m.HandleSessionRename},
		{"GET /api/session/{id}/permission-rules", m.HandleSessionPermissionRulesGet},
		{"PUT /api/session/{id}/permission-rules", m.HandleSessionPermissionRulesSet},
		{"PUT /api/session/{id}/native-permission-mode", m.HandleNativePermissionModeSet},
	} {
		r.Handle(route.pattern, route.handler)
	}
}
