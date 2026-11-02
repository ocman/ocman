package projectsmod

import (
	"context"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
)

// handleArchive archives or unarchives a project, keyed by its
// folded project-root directory. Project archive is independent of
// session archive state: a project stays hidden even with no current
// sessions, and auto-unarchives (server-side, in handleProjects) once a
// session's activity is newer than archived_at.
//
// Archive state is host-qualified: a project is (remoteID, root), because
// the same absolute path exists on every attached machine. An explicit
// remoteId wins; otherwise the owning host is resolved from the directory,
// exactly as the opencode-stop below is routed.
//
// POST /api/project/archive  { directory, archived, remoteId? }
func (m *Module) handleArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
		Archived  bool   `json:"archived"`
		RemoteID  string `json:"remoteId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	root := state.ProjectRootForDirectory(strings.TrimSpace(req.Directory))
	if root == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	remoteID := strings.TrimSpace(req.RemoteID)
	host, ok := m.d.ResolveOwner(w, root, remoteID)
	if !ok {
		return
	}
	remoteID = host.RemoteID()
	projects, err := m.List(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching projects", err)
		return
	}
	catalog := finalCatalog(projects)
	key := catalog.resolve(projectCheckout{Directory: root, RemoteID: remoteID}).Key
	type archiveTarget struct {
		host hostsvc.Host
		root string
	}
	targets := []archiveTarget{}
	for _, project := range projects {
		if project.ProjectKey != key {
			continue
		}
		owner, ok := m.d.ResolveOwner(w, project.Directory, state.NormalizeRemoteID(project.RemoteID))
		if !ok {
			return
		}
		targets = append(targets, archiveTarget{owner, project.Directory})
	}
	if len(targets) == 0 {
		targets = append(targets, archiveTarget{host, root})
	}

	for _, target := range targets {
		if req.Archived {
			// Best-effort: a dead tmux session, a removed/non-git directory
			// or an unreachable remote must not block the bookkeeping the
			// user actually asked for.
			if err := target.host.StopProjectOpencode(r.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: target.root}); err != nil {
				log.WithError(err).WithField("project", target.root).Warn("archive: stopping project opencode")
			}
			err = m.d.Store.ArchiveProject(r.Context(), target.host.RemoteID(), target.root)
		} else {
			err = m.d.Store.UnarchiveProject(r.Context(), target.host.RemoteID(), target.root)
		}
		if err != nil {
			httpx.ServerError(w, "updating archived project state", err)
			return
		}
	}

	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

// foldWorktreeProjects collapses <repo>/.worktrees/<repo>/<slug> directories
// back to the repo root and merges rows that fold to the same project,
// summing aggregate stats and keeping the newest activity. Rows are keyed
// per owning host (RemoteID) so identical paths on different machines stay
// separate. The folded root replaces the directory of the merged entry.
func foldWorktreeProjects(projects []db.ProjectStats) []db.ProjectStats {
	type key struct{ remoteID, root string }
	merged := make(map[key]*db.ProjectStats)
	order := make([]key, 0, len(projects))
	for _, p := range projects {
		k := key{p.RemoteID, state.ProjectRootForDirectory(p.Directory)}
		agg, ok := merged[k]
		if !ok {
			cp := p
			cp.Directory = k.root
			if cp.AssociatedDirectory != "" {
				cp.AssociatedDirectory = state.ProjectRootForDirectory(cp.AssociatedDirectory)
			}
			merged[k] = &cp
			order = append(order, k)
			continue
		}
		agg.SessionCount += p.SessionCount
		agg.UpstreamKeys = append(agg.UpstreamKeys, p.UpstreamKeys...)
		agg.MessageCount += p.MessageCount
		agg.TotalTokensIn += p.TotalTokensIn
		agg.TotalTokensOut += p.TotalTokensOut
		agg.TotalCost += p.TotalCost
		if p.LastUsed > agg.LastUsed {
			agg.LastUsed = p.LastUsed
		}
		agg.Archived = agg.Archived && p.Archived
	}
	out := make([]db.ProjectStats, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

// applyArchiveState overlays the Archived flag from state.db onto
// a project slice, folding directories to their project root. A project
// auto-unarchives (and the marker is deleted) when any of its sessions'
// activity (LastUsed) is newer than archived_at, mirroring the per-
// session archive semantics in applySessionState.
//
// Everything is keyed per owning host (remoteID, root), matching
// foldWorktreeProjects: the same path on two machines is two projects, so
// one host's archive must not hide the other's and activity on one must not
// unarchive the other.
func (m *Module) applyArchiveState(ctx context.Context, projects []db.ProjectStats) error {
	archived, err := m.d.Store.ArchivedProjects(ctx)
	if err != nil {
		return err
	}
	if len(archived) == 0 {
		return nil
	}

	// Newest activity per (host, folded root) across all matching directories.
	newest := map[state.ProjectKey]int64{}
	for _, p := range projects {
		key := ArchiveKey(p.RemoteID, p.Directory)
		if p.LastUsed > newest[key] {
			newest[key] = p.LastUsed
		}
	}

	// Auto-unarchive any project with newer activity than its archive
	// time, persisting the change so it stays unarchived.
	for key, archivedAt := range archived {
		if newest[key] > archivedAt {
			if err := m.d.Store.UnarchiveProject(ctx, key.RemoteID, key.Root); err != nil {
				return err
			}
			delete(archived, key)
		}
	}

	for i := range projects {
		if _, ok := archived[ArchiveKey(projects[i].RemoteID, projects[i].Directory)]; ok {
			projects[i].Archived = true
		}
	}
	return nil
}

// ArchiveKey builds a project's archive identity: its owning host
// plus its folded repo root. An unstamped RemoteID means the local machine.
func ArchiveKey(remoteID, directory string) state.ProjectKey {
	return state.ProjectKey{
		RemoteID: state.NormalizeRemoteID(remoteID),
		Root:     state.ProjectRootForDirectory(directory),
	}
}
