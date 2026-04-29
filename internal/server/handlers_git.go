package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/gitinfo"
)

// handleGitInfo returns per-directory git status (branch, ahead,
// behind, dirty) for every directory listed in the `dirs` query
// parameter. Used by the frontend components that show a branch
// indicator next to a session row, replacing the previous
// per-`/api/sessions`-request fork-fan-out which dragged the
// dashboard's tail latency to multi-second pauses (see
// docs/profiling.md).
//
// Query parameters:
//   - `dirs` (required): comma-separated absolute paths. At least
//     one path must be present and every path must be absolute,
//     mirroring handleGitDiff. A relative path anywhere in the list
//     rejects the whole request — partial trust is confusing.
//
// The response is a JSON object keyed by directory:
//
//	{
//	  "/abs/path/a": {"branch": "main", "ahead": 0, "behind": 0, "dirty": false},
//	  "/abs/path/b": {"branch": "feature", "ahead": 2, "behind": 0, "dirty": true}
//	}
//
// Non-repo directories are returned with the zero gitinfo.Info — the
// frontend treats Info{Branch: ""} as "not a repo".
//
// Status codes:
//
//	200 OK         — payload above
//	400 Bad Req    — `dirs` empty, or a path is relative
func (s *Server) handleGitInfo(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("dirs")
	if raw == "" {
		http.Error(w, "dirs query parameter is required", http.StatusBadRequest)
		return
	}
	parts := strings.Split(raw, ",")
	dirs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			http.Error(w, "every dir in dirs must be an absolute path", http.StatusBadRequest)
			return
		}
		dirs = append(dirs, p)
	}
	if len(dirs) == 0 {
		http.Error(w, "dirs query parameter is required", http.StatusBadRequest)
		return
	}

	infos := gitinfo.LookupMany(r.Context(), dirs)
	writeJSON(w, infos)
}

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
