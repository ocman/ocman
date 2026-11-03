package server

import (
	"context"
	"net/http"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/projectsmod"
	"github.com/ocman/ocman/internal/state"
)

// projectsModule shares one module across registry and listing callers. The
// remote inventory, owner resolution and queue are read at call time.
func (s *Server) projectsModule() *projectsmod.Module {
	return s.projectsMod.get(func() *projectsmod.Module {
		return projectsmod.NewModule(projectsmod.Deps{
			Store: s.stateDB,
			Local: func() hostsvc.Host { return s.router().Local() },
			ResolveOwner: func(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool) {
				return s.resolveOwner(w, dir, remoteID)
			},
			RemoteProjects: func() []db.ProjectStats {
				if s.remoteProjectsFn != nil {
					return s.remoteProjectsFn()
				}
				if s.remotes != nil {
					return s.remotes.RemoteProjects()
				}
				return nil
			},
			Broadcast:           s.broadcastGlobalEvent,
			DefaultAgent:        s.defaultAgent,
			ForgetProjectModels: func(dir string) { s.queueModule().ForgetProjectModels(dir) },
		})
	})
}

// listProjects is the projects module's checkout list.
func (s *Server) listProjects(ctx context.Context) ([]db.ProjectStats, error) {
	return s.projectsModule().List(ctx)
}

// projectRootForDirectory folds a session's directory back to the repo
// root that represents its "project" for grouping/archive purposes. It
// mirrors the frontend helper of the same name (frontend/src/lib/
// worktrees.ts) and internal/git.WorktreePathFor:
//
//	<prefix>/.worktrees/<repo>/<slug>...  ->  <prefix>/<repo>
//
// Any path that doesn't match the worktree layout is returned unchanged
// (trailing slash stripped), so unmanaged projects stay self-grouping.
func projectRootForDirectory(directory string) string {
	return state.ProjectRootForDirectory(directory)
}

// projectModelList returns dir's project model list (nil when none or
// unreadable) and its fallthrough off switch. Read errors are not cached
// so a transient failure heals.
func (s *Server) projectModelList(ctx context.Context, dir string) ([]string, bool) {
	return s.queueModule().ProjectModelList(ctx, dir)
}

// projectDefaultModel is the first configured model for dir's project.
func (s *Server) projectDefaultModel(ctx context.Context, dir string) string {
	if models, _ := s.projectModelList(ctx, dir); len(models) > 0 {
		return models[0]
	}
	return ""
}
