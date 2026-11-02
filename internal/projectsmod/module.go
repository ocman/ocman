// Package projectsmod owns the project list and the project bookkeeping
// routes: listing and identity resolution, archive, manual merge and the
// per-project settings. Projects are listed from this machine's host plus
// the remote inventory, then archive state, worktree folding, upstream
// grouping and saved merges are applied. Archive, merge and settings rows
// remain in internal/state; the projects index stays in the server.
package projectsmod

import (
	"context"
	"net/http"
	"sort"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
)

type Deps struct {
	Store *state.DB
	// Local is this machine's host, read at call time.
	Local func() hostsvc.Host
	// RemoteProjects returns the remote inventory's projects, tagged with
	// their owning remote; nil without remotes.
	RemoteProjects func() []db.ProjectStats
	// ResolveOwner resolves a directory's owning host, writing the error
	// response (503 for a disconnected named remote) when it fails.
	ResolveOwner func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool)
	// Broadcast sends a global SSE event.
	Broadcast func(event string, data []byte)
	// DefaultAgent is the hub-wide default agent setting.
	DefaultAgent func(ctx context.Context) (string, error)
	// ForgetProjectModels drops cached project model lists for dir.
	ForgetProjectModels func(dir string)
}

// Module is stateless; routes share the durable store.
type Module struct{ d Deps }

var _ module.Module = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{d: d} }
func (*Module) Name() string   { return "projects" }

func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/projects", m.handleProjects)
	r.Handle("POST /api/projects/resolve", m.handleResolveIdentities)
	// Project archive state (own state.db; no launch), same auth posture
	// as the per-session archive endpoint.
	r.Handle("POST /api/project/archive", m.handleArchive)
	r.Handle("POST /api/project/merge", m.handleMerge)
	// Per-project model list and defaults (own state.db setting rows).
	r.Setting("/api/project/settings", m.handleSettings)
}

func (m *Module) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := m.List(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching projects", err)
		return
	}
	httpx.WriteJSON(w, finalCatalog(projects).projects)
}

// List returns every project checkout, local and remote, with archive state,
// worktree folding, upstream keys and saved merges applied, newest first.
func (m *Module) List(ctx context.Context) ([]db.ProjectStats, error) {
	// Local projects flow through the host seam and owner-local statev2 history.
	projects, err := m.d.Local().Projects(ctx)
	if err != nil {
		return nil, err
	}
	// Append remote projects from the inventory cache, tagged with their
	// owning remote so the frontend can label them and target the right
	// machine when creating a session.
	if m.d.RemoteProjects != nil {
		projects = append(projects, m.d.RemoteProjects()...)
	}
	// Apply archive state after remotes are appended so remote projects
	// honour the archived_project markers too (they otherwise re-appear on
	// every refresh). Newer activity still auto-unarchives — most recent
	// timestamp wins.
	if err := m.applyArchiveState(ctx, projects); err != nil {
		return nil, err
	}
	// Fold worktree directories into their repo root and merge duplicate
	// rows so each project appears once, then sort by last activity.
	projects = foldWorktreeProjects(projects)
	remote.AssignProjectKeys(projects)
	if err := m.applyMerges(ctx, projects); err != nil {
		return nil, err
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].LastUsed > projects[j].LastUsed
	})
	if projects == nil {
		projects = []db.ProjectStats{} // [] not null for the frontend
	}
	return projects, nil
}
