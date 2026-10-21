package gitmod

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// handleGitInfo returns per-directory git status (branch, ahead,
// behind, dirty) for every directory listed in the `dirs` query
// parameter. Used by the frontend components that show a branch
// indicator next to a session row, replacing the previous
// per-`/api/sessions`-request fork-fan-out which dragged the
// dashboard's tail latency to multi-second pauses (see
// docs/other/profiling.md).
//
// Query parameters:
//   - `dir`: one literal absolute path, preserving commas and spaces. Takes
//     precedence over `dirs` when provided.
//   - `dirs` (required without `dir`): comma-separated absolute paths. At least
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
// Non-repo directories are returned with the zero git.Info — the
// frontend treats Info{Branch: ""} as "not a repo".
//
// Status codes:
//
//	200 OK         — payload above
//	400 Bad Req    — `dirs` empty, or a path is relative
func (m *Module) handleGitInfo(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("dirs")
	literal := r.URL.Query().Get("dir")
	if literal != "" {
		raw = literal
	}
	if raw == "" {
		http.Error(w, "dirs query parameter is required", http.StatusBadRequest)
		return
	}
	parts := []string{raw}
	if literal == "" {
		parts = strings.Split(raw, ",")
	}
	dirs := make([]string, 0, len(parts))
	for _, p := range parts {
		if literal == "" {
			p = strings.TrimSpace(p)
		}
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

	remoteID, ok := httpx.RemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	host, ok := m.Owner(w, dirs[0], remoteID)
	if !ok {
		return
	}
	observedAt := time.Now().UnixMilli()
	infos, err := host.GitInfo(r.Context(), dirs)
	if err != nil {
		log.WithError(err).Warn("git info failed")
		http.Error(w, "git info failed", http.StatusBadGateway)
		return
	}
	if m.OnGitInfo != nil {
		m.OnGitInfo(r.Context(), host.RemoteID(), infos, observedAt)
	}
	httpx.WriteJSON(w, infos)
}

// handleGitBranches returns the local branch names for the repository
// containing the `dir` query parameter, current branch first. Used by
// the composer's branch switcher.
//
// Query parameters:
//   - `dir` (required): absolute path inside a git repository.
//   - `remoteId` (optional): explicit owner; disconnected owners fail closed.
//
// Status codes:
//
//	200 OK         — {"branches": ["main", "feature", ...]}
//	400 Bad Req    — `dir` missing or relative
//	502 Bad Gateway — git invocation failed
//	503 Service Unavailable — explicit owner is disconnected
func (m *Module) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return
	}
	host, ok := m.Owner(w, dir, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	branches, err := host.GitBranches(r.Context(), dir)
	if err != nil {
		log.WithError(err).Warn("git branches failed")
		http.Error(w, "git branches failed", http.StatusBadGateway)
		return
	}
	if branches == nil {
		branches = []string{}
	}
	httpx.WriteJSON(w, map[string][]string{"branches": branches})
}

// handleGitCheckout switches the working tree in `dir` to `branch`. It
// is a mutation, so it is POST + localhost-only (the browser talks to
// the hub over localhost; the hub delegates to a remote over the Host
// seam transparently).
//
// Request body: {"dir": "/abs/path", "branch": "feature"}
//
// Status codes:
//
//	200 OK         — {"branch": "feature"}
//	400 Bad Req    — dir/branch missing or dir relative
//	409 Conflict   — checkout would overwrite local changes
//	502 Bad Gateway — git invocation failed
func (m *Module) handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
		return
	}
	if body.Dir == "" || body.Branch == "" {
		http.Error(w, "dir and branch are required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(body.Dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}
	err := m.ForDir(body.Dir).GitCheckout(r.Context(), body.Dir, body.Branch)
	if err != nil {
		// ErrDirtyCheckout doesn't survive gRPC as a typed sentinel, so
		// also match on the message a remote host relays back.
		if errors.Is(err, git.ErrDirtyCheckout) ||
			strings.Contains(err.Error(), "would overwrite") ||
			strings.Contains(err.Error(), "commit your changes or stash") {
			http.Error(w, "checkout would overwrite local changes; commit or stash first", http.StatusConflict)
			return
		}
		log.WithError(err).Warn("git checkout failed")
		http.Error(w, "git checkout failed", http.StatusBadGateway)
		return
	}
	httpx.WriteJSON(w, map[string]string{"branch": body.Branch})
}

// handleGitDiff returns the working-tree diff for a directory. The
// directory is taken from the `dir` query parameter — directory-
// scoped, not session-scoped, because the diff is a property of the
// worktree, not of any one conversation. Two sessions in the same
// project share the same diff.
//
// Query parameters:
//   - `dir` (required): absolute path to a directory inside a git
//     git. Relative paths are rejected to avoid relying on the
//     server's CWD.
//   - `fresh=1` (optional): bypass the in-process cache. The cache
//     TTL is short (1s) and exists mainly to coalesce concurrent
//     requests; the frontend's SSE-driven refetch path passes
//     fresh=1 so an edit-event-triggered refresh is never served
//     stale data.
//
// Status codes:
//
//	200 OK         — diff payload (git.Diff JSON)
//	400 Bad Req    — `dir` missing or relative
//	404 Not Found  — `dir` is not a git worktree
//	502 Bad Gateway — git invocation failed (timeout, fork error)
func (m *Module) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return
	}
	fresh := r.URL.Query().Get("fresh") == "1"

	d, err := m.ForDir(dir).GitDiff(r.Context(), dir, hostsvc.GitDiffOptions{Force: fresh})
	if err != nil {
		// Git can replace cancellation with ErrNotRepo or a killed-process
		// exit error, so check the request before interpreting its result.
		if httpx.WriteCancellation(w, "git diff canceled", r.Context().Err()) || httpx.WriteCancellation(w, "git diff canceled", err) {
			return
		}
		if errors.Is(err, git.ErrNotRepo) {
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
	httpx.WriteJSON(w, d)
}
