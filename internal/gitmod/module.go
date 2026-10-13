// Package gitmod is the git and worktree feature module: per-directory git
// status, branches, checkout and working-tree diffs, and the worktree list,
// default base ref, removal and create-and-launch controls.
//
// Every operation runs on the owning machine's hostsvc.Host (git through
// internal/gitexec there); the module never runs git or tmux itself. A
// client-named owner is resolved through Deps.Owner, which fails closed
// when that remote is not connected.
package gitmod

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Deps is what the composition root supplies. Functions are read at call
// time, so connected remotes may change after the module is built.
type Deps struct {
	// Owner resolves an explicit owner, writing the rejection itself (503
	// remote_not_connected) when that remote is not connected; an empty
	// remoteID infers the owner from dir.
	Owner func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool)
	// ForDir infers the owner of dir, for routes whose clients name none.
	ForDir func(dir string) hostsvc.Host
	// ResolvePlatform picks the new session's platform on host: the requested
	// one, else the first prompt's model reference, else the parent session's,
	// else the owner's default. It returns the compound id and the base id.
	ResolvePlatform func(r *http.Request, host hostsvc.Host, requested, model, parentSessionID string) (id, base string, err error)
	// InheritPermissions reads the parent's inheritable permissions (it runs
	// alongside worktree creation) and returns the step that applies them to
	// the child, reporting the rule count and a soft-fail note.
	InheritPermissions func(r *http.Request, platform, parentSessionID string) func(childSessionID string) (int, string)
	// SendNow delivers the first message immediately, never queued.
	SendNow func(ctx context.Context, platformID string, req platforms.SendMessageRequest) error
	Guards  module.Guards
}

// Module serves /api/git/{info,branches,checkout,diff} and
// /api/worktree/{list,default-base-ref,remove,create-and-launch}.
type Module struct{ Deps }

var _ module.Module = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{Deps: d} }

func (m *Module) Name() string { return "git" }

// Routes keeps the method check outside the auth guard, as the server's
// requireGET/requirePOST do, so a wrong method is 405 for every caller.
// Reads need a login; mutations and launches are localhost-only.
func (m *Module) Routes(r module.Router) {
	auth, local := m.Guards.Auth, m.Guards.Localhost
	r.Handle("/api/git/diff", only(http.MethodGet, auth(m.handleGitDiff)))
	r.Handle("/api/git/info", only(http.MethodGet, auth(m.handleGitInfo)))
	r.Handle("/api/git/branches", only(http.MethodGet, auth(m.handleGitBranches)))
	r.Handle("/api/git/checkout", only(http.MethodPost, local(m.handleGitCheckout)))
	r.Handle("/api/worktree/list", only(http.MethodGet, auth(m.handleWorktreeList)))
	r.Handle("/api/worktree/default-base-ref", only(http.MethodGet, auth(m.handleWorktreeDefaultBaseRef)))
	r.Handle("/api/worktree/create-and-launch", only(http.MethodPost, local(m.handleWorktreeCreateAndLaunch)))
	r.Handle("/api/worktree/remove", only(http.MethodPost, local(m.handleWorktreeRemove)))
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
