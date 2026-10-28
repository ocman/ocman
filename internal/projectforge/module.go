// Package projectforge is the PRs & Issues feature module: a project's
// upstream detection, its forge PR/issue lists, CI checks, mergeability and
// forge identity, the "handle this" launch, and the prompt templates that
// launch renders.
//
// Every /api/project/* route takes an explicit owner (remoteId, "local" for
// the hub); the prompt templates are hub-wide settings. The
// owner's host detects the repository and fetches cross-fork heads; the hub
// classifies remotes and talks to the forge with its own clients. The module
// never runs git itself.
package projectforge

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
)

// Store persists the prompt templates.
type Store interface {
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Sessions is the server's shared session mutation path.
type Sessions interface {
	Create(ctx context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	SendMessage(ctx context.Context, platformID string, req platforms.SendMessageRequest) error
}

// Deps is what the composition root supplies. Functions are read at call
// time, so forge configuration and connected remotes may change after the
// module is built.
type Deps struct {
	// Store holds the prompt templates; nil serves the defaults and refuses
	// to save.
	Store Store
	// Owner resolves the explicit owner, writing the rejection itself
	// (503 remote_not_connected) when that remote is not connected.
	Owner func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool)
	// ResolveForge returns the hub's client for a classified remote.
	ResolveForge func(forge.Remote) (forge.Forge, bool)
	// KnowsForgejo reports whether the hub has a Forgejo client for host.
	KnowsForgejo func(host string) bool
	// CreatePlatform resolves a requested platform, else the model
	// reference's, else the owner's default, into its compound ID and base.
	CreatePlatform func(ctx context.Context, host hostsvc.Host, requested, model string) (id, base string, err error)
	// Registered reports whether a platform adapter is registered.
	Registered func(platformID string) bool
	Sessions   Sessions
}

// Module serves /api/project/{upstreams,prs,issues,pr-checks,
// pr-mergeability,forge-user,handle} and /api/settings/prompt-templates.
type Module struct {
	Deps
	upstreams upstreamCache
}

func NewModule(d Deps) *Module {
	return &Module{Deps: d, upstreams: upstreamCache{now: time.Now}}
}

func (m *Module) Name() string { return "project-forge" }

// Routes keeps the method check outside the auth guard, as the server's
// method-pattern routes do, so a wrong method is 405 for every caller.
func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/project/upstreams", m.handleUpstreams)
	r.Local("GET /api/project/prs", m.handlePRs)
	r.Local("GET /api/project/issues", m.handleIssues)
	r.Local("GET /api/project/pr-checks", m.handlePRChecks)
	r.Local("GET /api/project/pr-mergeability", m.handlePRMergeability)
	r.Local("GET /api/project/forge-user", m.handleForgeUser)
	// Launch: spawns an OpenCode session or worktree on the owner.
	r.Local("POST /api/project/handle", m.handleHandle)
	r.Setting("/api/settings/prompt-templates", m.handlePromptTemplates)
}

// classify keeps the remotes the hub can serve, typed by its configuration.
func (m *Module) classify(remotes []forge.Remote) []forge.Remote {
	return classifyRemotes(remotes, func(host string) bool {
		return m.KnowsForgejo != nil && m.KnowsForgejo(host)
	})
}

func (m *Module) resolveForge(rem forge.Remote) (forge.Forge, bool) {
	if m.ResolveForge == nil {
		return nil, false
	}
	return m.ResolveForge(rem)
}
