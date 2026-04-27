package server

import (
	"errors"
	"net/http"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/gitinfo"
)

// handleGitDiff returns the working-tree diff for a directory. The
// directory is taken from the `dir` query parameter — directory-
// scoped, not session-scoped, because the diff is a property of the
// worktree, not of any one conversation. Two sessions in the same
// project share the same diff.
//
// Query parameters:
//   - `dir` (required): absolute path to a directory inside a git
//     worktree. Relative paths are rejected to avoid relying on the
//     server's CWD.
//   - `fresh=1` (optional): bypass the in-process cache. The cache
//     TTL is short (1s) and exists mainly to coalesce concurrent
//     requests; the frontend's SSE-driven refetch path passes
//     fresh=1 so an edit-event-triggered refresh is never served
//     stale data.
//
// Status codes:
//
//	200 OK         — diff payload (gitinfo.Diff JSON)
//	400 Bad Req    — `dir` missing or relative
//	404 Not Found  — `dir` is not a git worktree
//	502 Bad Gateway — git invocation failed (timeout, fork error)
func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}
	fresh := r.URL.Query().Get("fresh") == "1"

	d, err := gitinfo.GetDiff(r.Context(), dir, gitinfo.DiffOptions{Force: fresh})
	if err != nil {
		if errors.Is(err, gitinfo.ErrNotRepo) {
			http.Error(w, "directory is not a git worktree", http.StatusNotFound)
			return
		}
		// Generic upstream/git failure (binary missing, timeout,
		// permission error). 502 mirrors how platform errors are
		// surfaced — "we tried to talk to an external thing and
		// it didn't work".
		log.WithError(err).Warn("git diff failed")
		http.Error(w, "git diff failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, d)
}
