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

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
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
	// PlatformFor is the compound OpenCode platform id of a host.
	PlatformFor func(hostsvc.Host) string
	// Registered reports whether a platform adapter is registered.
	Registered func(platformID string) bool
	Sessions   Sessions
	Guards     module.Guards
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
// requireGET/requirePOST do, so a wrong method is 405 for every caller.
func (m *Module) Routes(r module.Router) {
	auth, local := m.Guards.Auth, m.Guards.Localhost
	r.Handle("/api/project/upstreams", only(http.MethodGet, auth(m.handleUpstreams)))
	r.Handle("/api/project/prs", only(http.MethodGet, local(m.handlePRs)))
	r.Handle("/api/project/issues", only(http.MethodGet, local(m.handleIssues)))
	r.Handle("/api/project/pr-checks", only(http.MethodGet, local(m.handlePRChecks)))
	r.Handle("/api/project/pr-mergeability", only(http.MethodGet, local(m.handlePRMergeability)))
	r.Handle("/api/project/forge-user", only(http.MethodGet, local(m.handleForgeUser)))
	// Launch: spawns an OpenCode session or worktree on the owner.
	r.Handle("/api/project/handle", only(http.MethodPost, local(m.handleHandle)))
	r.Setting("/api/settings/prompt-templates", auth(m.handlePromptTemplates))
}

func only(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
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
