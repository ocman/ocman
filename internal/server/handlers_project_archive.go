package server

import (
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

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

// handleProjectArchive archives or unarchives a project, keyed by its
// folded project-root directory. Project archive is independent of
// session archive state: a project stays hidden even with no current
// sessions, and auto-unarchives (server-side, in handleProjects) once a
// session's activity is newer than archived_at.
//
// POST /api/project/archive  { directory, archived }
func (s *Server) handleProjectArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
		Archived  bool   `json:"archived"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	root := projectRootForDirectory(strings.TrimSpace(req.Directory))
	if root == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}

	var err error
	if req.Archived {
		// Best-effort: a dead tmux session, a removed/non-git directory
		// or an unreachable remote must not block the bookkeeping the
		// user actually asked for.
		if err := s.router().ForDir(root).StopProjectOpencode(r.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}); err != nil {
			log.WithError(err).WithField("project", root).Warn("archive: stopping project opencode")
		}
		err = s.stateDB.ArchiveProject(root)
	} else {
		err = s.stateDB.UnarchiveProject(root)
	}
	if err != nil {
		serverError(w, "updating archived project state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}
