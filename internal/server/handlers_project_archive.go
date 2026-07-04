package server

import (
	"net/http"
	"strings"
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
	if directory == "" {
		return directory
	}
	cleaned := directory
	if len(cleaned) > 1 && strings.HasSuffix(cleaned, "/") {
		cleaned = cleaned[:len(cleaned)-1]
	}

	parts := strings.Split(cleaned, "/")
	idx := -1
	for i, p := range parts {
		if p == ".worktrees" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return cleaned
	}
	// Need at least <prefix>/.worktrees/<repo>/<slug>.
	if len(parts) < idx+3 {
		return cleaned
	}
	// idx==0 (relative path starting with .worktrees) has no prefix.
	if idx == 0 {
		return cleaned
	}
	prefix := strings.Join(parts[:idx], "/")
	// Absolute path directly under "/" (prefix=="" from leading slash):
	// can't distinguish from a real "/repo", so don't fold.
	if prefix == "" {
		return cleaned
	}
	return prefix + "/" + parts[idx+1]
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
