package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// detectUpstreams classifies the git remotes of the project at
// projectDir, returning a list of supported forges. Composes
// worktree.ResolveRepoRoot (so callers can pass any directory inside
// the repo) with forge.Detect (which expects the repo root).
//
// Hoisted out of the handler so the launch path (POST /api/project/
// handle) can reuse the exact same resolution.
func (s *Server) detectUpstreams(ctx context.Context, projectDir string) (string, []forge.Remote, error) {
	repoRoot, err := worktree.ResolveRepoRoot(ctx, projectDir)
	if err != nil {
		return "", nil, err
	}
	var hosts forge.ForgejoHostMap
	if s.integrations != nil && s.integrations.Forgejo != nil {
		hosts = s.integrations.Forgejo
	}
	remotes, err := forge.Detect(ctx, repoRoot, hosts)
	if err != nil {
		return repoRoot, nil, err
	}
	return repoRoot, remotes, nil
}

// resolveForge returns the Forge implementation that handles the given
// remote, plus a boolean indicating whether one was found. Returns
// (nil, false) for remotes whose type is unsupported or whose host
// has no configured client.
func (s *Server) resolveForge(rem forge.Remote) (forge.Forge, bool) {
	if s.integrations == nil {
		return nil, false
	}
	switch rem.Type {
	case forge.RemoteTypeGitHub:
		if s.integrations.GitHub == nil {
			return nil, false
		}
		return s.integrations.GitHub, true
	case forge.RemoteTypeForgejo:
		if s.integrations.Forgejo == nil {
			return nil, false
		}
		c := s.integrations.Forgejo.ForHost(rem.Host)
		if c == nil {
			return nil, false
		}
		return c, true
	}
	return nil, false
}

// findRemote locates a remote by name in the detected list. Returns
// (zero, false) when no remote matches.
func findRemote(remotes []forge.Remote, name string) (forge.Remote, bool) {
	for _, r := range remotes {
		if r.Name == name {
			return r, true
		}
	}
	return forge.Remote{}, false
}

// handleProjectUpstreams responds with the supported upstreams for
// the project containing dir.
//
// GET /api/project/upstreams?dir=<abs>
//
// Response 200: { "upstreams": [{remote, host, type, repo}, ...] }
// Response 404: dir is not in a git repo.
// Response 400: dir param missing / not absolute.
func (s *Server) handleProjectUpstreams(w http.ResponseWriter, r *http.Request) {
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	_, remotes, err := s.detectUpstreams(r.Context(), dir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("upstream: detect failed")
		http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		return
	}

	// Always emit a non-nil slice so the JSON is `[]` not `null`.
	if remotes == nil {
		remotes = []forge.Remote{}
	}
	writeJSON(w, map[string]interface{}{"upstreams": remotes})
}

// handleProjectPRs lists PRs for one remote of the current project.
// GET /api/project/prs?dir=<abs>&remote=<name>&state=<open|closed|all>&mine=<login>&page=<n>
//
// Mine is a *login* (not a boolean): the frontend resolves the
// "current user" per host via /api/project/forge-user and passes the
// login back in. This keeps the backend stateless (no "who is the
// current user per host" cache) and avoids re-fetching the user on
// every list call.
func (s *Server) handleProjectPRs(w http.ResponseWriter, r *http.Request) {
	dir, rem, opts, ok := s.parseProjectListParams(w, r)
	if !ok {
		return
	}
	_ = dir // unused after parseProjectListParams resolves the remote

	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required",
			"no forge client configured for "+rem.Host)
		return
	}

	prs, rl, err := f.ListPRs(r.Context(), rem.Repo, opts)
	if err != nil {
		writeProjectListError(w, http.StatusBadGateway, "upstream_status", err.Error())
		return
	}

	// Apply mine filtering when the caller asked for it. Some forges
	// support server-side filtering via creator/assignee params, but
	// for simplicity (and to keep "OR requested_reviewer" working)
	// we post-filter here. The page size is bounded so the cost is
	// negligible.
	if opts.Mine != "" {
		prs = filterPRsForUser(prs, opts.Mine)
	}

	if rl.Limited {
		writeJSON(w, map[string]interface{}{
			"prs":        prs,
			"pagination": map[string]interface{}{"page": opts.Page, "hasMore": false},
			"rateLimit":  rl,
		})
		return
	}
	writeJSON(w, map[string]interface{}{
		"prs":        prs,
		"pagination": map[string]interface{}{"page": opts.Page, "hasMore": len(prs) >= effectivePerPage(opts)},
		"rateLimit":  rl,
	})
}

// handleProjectIssues lists issues for one remote of the current project.
// GET /api/project/issues?dir=<abs>&remote=<name>&state=<open|closed|all>&mine=<login>&page=<n>
func (s *Server) handleProjectIssues(w http.ResponseWriter, r *http.Request) {
	dir, rem, opts, ok := s.parseProjectListParams(w, r)
	if !ok {
		return
	}
	_ = dir

	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required",
			"no forge client configured for "+rem.Host)
		return
	}

	issues, rl, err := f.ListIssues(r.Context(), rem.Repo, opts)
	if err != nil {
		writeProjectListError(w, http.StatusBadGateway, "upstream_status", err.Error())
		return
	}

	if opts.Mine != "" {
		issues = filterIssuesForUser(issues, opts.Mine)
	}

	if rl.Limited {
		writeJSON(w, map[string]interface{}{
			"issues":     issues,
			"pagination": map[string]interface{}{"page": opts.Page, "hasMore": false},
			"rateLimit":  rl,
		})
		return
	}
	writeJSON(w, map[string]interface{}{
		"issues":     issues,
		"pagination": map[string]interface{}{"page": opts.Page, "hasMore": len(issues) >= effectivePerPage(opts)},
		"rateLimit":  rl,
	})
}

// handleProjectForgeUser returns the authenticated user's login for a
// given remote's host, so the frontend can pass it back as the `mine`
// filter without storing forge tokens client-side.
//
// GET /api/project/forge-user?dir=<abs>&remote=<name>
//
// 200: { "login": "alice", "host": "github.com" }
// 401: caller is unauthenticated against this forge.
func (s *Server) handleProjectForgeUser(w http.ResponseWriter, r *http.Request) {
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	if dir == "" || remoteName == "" {
		http.Error(w, "dir and remote are required", http.StatusBadRequest)
		return
	}

	_, remotes, err := s.detectUpstreams(r.Context(), dir)
	if err != nil {
		http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		return
	}
	rem, ok := findRemote(remotes, remoteName)
	if !ok {
		http.Error(w, "remote not found among project upstreams", http.StatusNotFound)
		return
	}
	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required",
			"no forge client configured for "+rem.Host)
		return
	}
	u, err := f.CurrentUser(r.Context())
	if err != nil {
		if errors.Is(err, forge.ErrUnauthenticated) {
			writeProjectListError(w, http.StatusUnauthorized, "auth_required", "not authenticated")
			return
		}
		writeProjectListError(w, http.StatusBadGateway, "upstream_status", err.Error())
		return
	}
	writeJSON(w, u)
}

// parseProjectListParams decodes the shared query parameters of the
// PR/Issue list endpoints: dir, remote, state, mine, page. Validates
// each, writes the appropriate error response on failure, and resolves
// the remote into a forge.Remote so the caller can dispatch to the
// right Forge implementation.
func (s *Server) parseProjectListParams(w http.ResponseWriter, r *http.Request) (string, forge.Remote, forge.ListOptions, bool) {
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	mine := strings.TrimSpace(r.URL.Query().Get("mine"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if dir == "" || remoteName == "" {
		http.Error(w, "dir and remote query parameters are required", http.StatusBadRequest)
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	switch state {
	case "", "open", "closed", "all":
		// ok
	default:
		http.Error(w, "state must be one of open|closed|all", http.StatusBadRequest)
		return "", forge.Remote{}, forge.ListOptions{}, false
	}

	_, remotes, err := s.detectUpstreams(r.Context(), dir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
		} else {
			http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		}
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	rem, ok := findRemote(remotes, remoteName)
	if !ok {
		http.Error(w, "remote not found among project upstreams", http.StatusNotFound)
		return "", forge.Remote{}, forge.ListOptions{}, false
	}

	return dir, rem, forge.ListOptions{
		State: state,
		Mine:  mine,
		Page:  page,
	}, true
}

// effectivePerPage mirrors the adapters' default — used by the
// hasMore heuristic.
func effectivePerPage(opts forge.ListOptions) int {
	if opts.PerPage > 0 {
		return opts.PerPage
	}
	// Matches the per-page default in both adapters.
	return 30
}

// filterPRsForUser keeps PRs where login is author, an assignee, or
// a requested reviewer. Implements FR-5's "mine" semantics.
func filterPRsForUser(prs []forge.PR, login string) []forge.PR {
	out := prs[:0]
	for _, p := range prs {
		if matchesUser(p.Author, login) {
			out = append(out, p)
			continue
		}
		if anyUserMatches(p.Assignees, login) ||
			anyUserMatches(p.RequestedReviewers, login) {
			out = append(out, p)
		}
	}
	return out
}

// filterIssuesForUser keeps issues where login is author or assignee.
func filterIssuesForUser(issues []forge.Issue, login string) []forge.Issue {
	out := issues[:0]
	for _, i := range issues {
		if matchesUser(i.Author, login) {
			out = append(out, i)
			continue
		}
		if anyUserMatches(i.Assignees, login) {
			out = append(out, i)
		}
	}
	return out
}

func matchesUser(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func anyUserMatches(users []forge.User, login string) bool {
	for _, u := range users {
		if matchesUser(u.Login, login) {
			return true
		}
	}
	return false
}

// writeProjectListError emits the structured error envelope documented
// in spec/pr-issue-sidebar/architecture.md's "API design" section.
//
// Sets Content-Type and the status code BEFORE encoding so the
// envelope shape stays stable even when json.Encode hits an error.
func writeProjectListError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
			"status":  status,
		},
	})
}


