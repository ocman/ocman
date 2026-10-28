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

	"github.com/ocman/ocman/internal/git"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
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
	SendNow   func(ctx context.Context, platformID string, req platforms.SendMessageRequest) error
	OnGitInfo func(context.Context, string, map[string]git.Info, int64)
}

// Module serves /api/git/{info,branches,checkout,diff} and
// /api/worktree/{list,default-base-ref,remove,create-and-launch}.
type Module struct{ Deps }

var _ module.Module = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{Deps: d} }

func (m *Module) Name() string { return "git" }

// Routes uses method patterns, so the mux answers a wrong method with 405
// before any guard runs. Reads need a login; mutations and launches are
// localhost-only.
func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/git/diff", m.handleGitDiff)
	r.Handle("GET /api/git/info", m.handleGitInfo)
	r.Handle("GET /api/git/branches", m.handleGitBranches)
	r.Local("POST /api/git/checkout", m.handleGitCheckout)
	r.Handle("GET /api/worktree/list", m.handleWorktreeList)
	r.Handle("GET /api/worktree/default-base-ref", m.handleWorktreeDefaultBaseRef)
	r.Local("POST /api/worktree/create-and-launch", m.handleWorktreeCreateAndLaunch)
	r.Local("POST /api/worktree/remove", m.handleWorktreeRemove)
}
