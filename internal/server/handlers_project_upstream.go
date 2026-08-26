package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

const (
	projectUpstreamsTTL        = 10 * time.Second
	projectUpstreamsTimeout    = 10 * time.Second
	projectUpstreamsMax        = 128
	projectUpstreamsConcurrent = 8
)

type projectUpstreamsCacheEntry struct {
	value     *hostsvc.ProjectUpstreams
	expiresAt time.Time
}

type projectUpstreamsPending struct {
	done    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	waiters int
	value   *hostsvc.ProjectUpstreams
	err     error
}

// detectUpstreams asks the resolved owner to inspect its repository and
// returns only normalized repository/forge data to the hub.
func (s *Server) detectUpstreams(ctx context.Context, host hostsvc.Host, projectDir string) (string, []forge.Remote, error) {
	key := host.RemoteID() + "\x00" + projectDir
	now := time.Now()
	if s.upstreamNow != nil {
		now = s.upstreamNow()
	}
	s.projectUpstreamsMu.Lock()
	cached, ok := s.projectUpstreams[key]
	if ok && now.Before(cached.expiresAt) {
		s.projectUpstreamsMu.Unlock()
		return cached.value.RepoRoot, cached.value.Remotes, nil
	}
	if pending := s.projectUpstreamsPending[key]; pending != nil {
		pending.waiters++
		s.projectUpstreamsMu.Unlock()
		return s.waitProjectUpstreams(ctx, key, pending)
	}
	if err := ctx.Err(); err != nil {
		s.projectUpstreamsMu.Unlock()
		return "", nil, err
	}
	if s.projectUpstreamsSlots == nil {
		s.projectUpstreamsSlots = make(chan struct{}, projectUpstreamsConcurrent)
	}
	slots := s.projectUpstreamsSlots
	if s.projectUpstreamsPending == nil {
		s.projectUpstreamsPending = make(map[string]*projectUpstreamsPending)
	}
	if len(s.projectUpstreamsPending) >= projectUpstreamsMax {
		s.projectUpstreamsMu.Unlock()
		return "", nil, errors.New("too many upstream detections in progress")
	}
	pendingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), projectUpstreamsTimeout)
	pending := &projectUpstreamsPending{done: make(chan struct{}), ctx: pendingCtx, cancel: cancel, waiters: 1}
	s.projectUpstreamsPending[key] = pending
	s.projectUpstreamsMu.Unlock()
	go s.loadProjectUpstreams(key, projectDir, host, slots, pending)
	return s.waitProjectUpstreams(ctx, key, pending)
}

func (s *Server) loadProjectUpstreams(key, projectDir string, host hostsvc.Host, slots chan struct{}, pending *projectUpstreamsPending) {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-pending.ctx.Done():
		s.finishProjectUpstreams(key, pending, nil, pending.ctx.Err())
		return
	}
	upstreams, err := host.ProjectUpstreams(pending.ctx, projectDir)
	if err == nil && upstreams == nil {
		err = errors.New("owner returned no upstream result")
	}
	if err == nil && !filepath.IsAbs(upstreams.RepoRoot) {
		err = errors.New("owner returned invalid repository root")
	}
	if err == nil {
		upstreams.Remotes = s.classifyProjectRemotes(upstreams.Remotes)
	}
	s.finishProjectUpstreams(key, pending, upstreams, err)
}

func (s *Server) finishProjectUpstreams(key string, pending *projectUpstreamsPending, upstreams *hostsvc.ProjectUpstreams, err error) {
	s.projectUpstreamsMu.Lock()
	defer s.projectUpstreamsMu.Unlock()
	current := s.projectUpstreamsPending[key] == pending
	if err == nil && current {
		expiresAt := time.Now()
		if s.upstreamNow != nil {
			expiresAt = s.upstreamNow()
		}
		if s.projectUpstreams == nil {
			s.projectUpstreams = make(map[string]projectUpstreamsCacheEntry)
		}
		for len(s.projectUpstreams) >= projectUpstreamsMax {
			for oldKey := range s.projectUpstreams {
				delete(s.projectUpstreams, oldKey)
				break
			}
		}
		s.projectUpstreams[key] = projectUpstreamsCacheEntry{value: upstreams, expiresAt: expiresAt.Add(projectUpstreamsTTL)}
	}
	pending.value, pending.err = upstreams, err
	pending.cancel()
	if current {
		delete(s.projectUpstreamsPending, key)
	}
	close(pending.done)
}

func (s *Server) waitProjectUpstreams(ctx context.Context, key string, pending *projectUpstreamsPending) (string, []forge.Remote, error) {
	defer func() {
		s.projectUpstreamsMu.Lock()
		pending.waiters--
		if pending.waiters == 0 && s.projectUpstreamsPending[key] == pending {
			delete(s.projectUpstreamsPending, key)
			pending.cancel()
		}
		s.projectUpstreamsMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case <-pending.done:
		if pending.err != nil {
			return "", nil, pending.err
		}
		return pending.value.RepoRoot, pending.value.Remotes, nil
	}
}

func (s *Server) classifyProjectRemotes(remotes []forge.Remote) []forge.Remote {
	out := make([]forge.Remote, 0, len(remotes))
	for _, rem := range remotes {
		rem.URL = ""
		if !validProjectRemote(rem) {
			continue
		}
		switch {
		case rem.Host == "github.com":
			rem.Type = forge.RemoteTypeGitHub
		case s.integrations != nil && s.integrations.Forgejo != nil && s.integrations.Forgejo.Knows(rem.Host):
			rem.Type = forge.RemoteTypeForgejo
		default:
			continue
		}
		out = append(out, rem)
	}
	return out
}

func validProjectRemote(rem forge.Remote) bool {
	if strings.TrimSpace(rem.Name) == "" || strings.TrimSpace(rem.Host) == "" || strings.ContainsAny(rem.Host, "/\\?#@") {
		return false
	}
	parts := strings.Split(rem.Repo, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if !validProjectRepoRune(r) {
				return false
			}
		}
	}
	return true
}

func validProjectRepoRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r)
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
// GET /api/project/upstreams?dir=<abs>&remoteId=<owner>
//
// Response 200: { "upstreams": [{remote, host, type, repo}, ...] }
// Response 404: dir is not in a git repo.
// Response 400: dir param missing / not absolute.
func (s *Server) handleProjectUpstreams(w http.ResponseWriter, r *http.Request) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}
	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}

	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return
	}
	_, remotes, err := s.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
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
// GET /api/project/prs?dir=<abs>&remote=<name>&remoteId=<owner>&state=<open|closed|all>&mine=<login>&page=<n>
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
	hasMore := !rl.Limited && len(prs) >= effectivePerPage(opts)

	// Apply mine filtering when the caller asked for it. Some forges
	// support server-side filtering via creator/assignee params, but
	// for simplicity (and to keep "OR requested_reviewer" working)
	// we post-filter here. The page size is bounded so the cost is
	// negligible.
	if opts.Mine != "" {
		prs = filterPRsForUser(prs, opts.Mine)
	}

	writeForgeListResponse(w, "prs", prs, rl, opts, hasMore)
}

// handleProjectIssues lists issues for one remote of the current project.
// GET /api/project/issues?dir=<abs>&remote=<name>&remoteId=<owner>&state=<open|closed|all>&mine=<login>&page=<n>
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
	hasMore := !rl.Limited && len(issues) >= effectivePerPage(opts)

	if opts.Mine != "" {
		issues = filterIssuesForUser(issues, opts.Mine)
	}

	writeForgeListResponse(w, "issues", issues, rl, opts, hasMore)
}

// writeForgeListResponse writes the shared { <key>, pagination,
// rateLimit } envelope for the PR/Issue list endpoints. hasMore is
// false when rate-limited (the page is incomplete) and otherwise uses
// the "full page implies more" heuristic.
func writeForgeListResponse[T any](w http.ResponseWriter, key string, items []T, rl forge.RateLimit, opts forge.ListOptions, hasMore bool) {
	writeJSON(w, map[string]interface{}{
		key:          items,
		"pagination": map[string]interface{}{"page": opts.Page, "hasMore": hasMore},
		"rateLimit":  rl,
	})
}

// handleProjectForgeUser returns the authenticated user's login for a
// given remote's host, so the frontend can pass it back as the `mine`
// filter without storing forge tokens client-side.
//
// GET /api/project/forge-user?dir=<abs>&remote=<name>&remoteId=<owner>
//
// 200: { "login": "alice", "host": "github.com" }
// 401: caller is unauthenticated against this forge.
func (s *Server) handleProjectForgeUser(w http.ResponseWriter, r *http.Request) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	if remoteName == "" {
		http.Error(w, "remote is required", http.StatusBadRequest)
		return
	}
	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return
	}
	_, remotes, err := s.detectUpstreams(r.Context(), host, dir)
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

// handleProjectPRChecks returns the combined CI/build status for a
// PR's head commit. Fetched lazily by the sidebar when a PR row is
// expanded/hovered, so the list endpoint stays cheap.
//
// GET /api/project/pr-checks?dir=<abs>&remote=<name>&remoteId=<owner>&sha=<headSha>
//
// 200: { "state": "success|pending|failure|unknown", "checks": [...] }
func (s *Server) handleProjectPRChecks(w http.ResponseWriter, r *http.Request) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	sha := strings.TrimSpace(r.URL.Query().Get("sha"))
	if remoteName == "" || sha == "" {
		http.Error(w, "remote and sha query parameters are required", http.StatusBadRequest)
		return
	}
	if !validCommitSHA(sha) {
		http.Error(w, "sha must be a hexadecimal commit ID", http.StatusBadRequest)
		return
	}
	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return
	}
	_, remotes, err := s.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
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

	ci, rl, err := f.Checks(r.Context(), rem.Repo, sha)
	if err != nil {
		writeProjectListError(w, http.StatusBadGateway, "upstream_status", err.Error())
		return
	}
	if ci.Checks == nil {
		ci.Checks = []forge.Check{}
	}
	writeJSON(w, map[string]interface{}{
		"state":     ci.State,
		"checks":    ci.Checks,
		"rateLimit": rl,
	})
}

func validCommitSHA(sha string) bool {
	if len(sha) < 3 || len(sha) > 64 {
		return false
	}
	for _, r := range sha {
		if !validHexRune(r) {
			return false
		}
	}
	return true
}

func validHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// parseProjectListParams decodes the shared query parameters of the
// PR/Issue list endpoints: dir, remote, state, mine, page. Validates
// each, writes the appropriate error response on failure, and resolves
// the remote into a forge.Remote so the caller can dispatch to the
// right Forge implementation.
func (s *Server) parseProjectListParams(w http.ResponseWriter, r *http.Request) (string, forge.Remote, forge.ListOptions, bool) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	mine := strings.TrimSpace(r.URL.Query().Get("mine"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if remoteName == "" {
		http.Error(w, "remote query parameter is required", http.StatusBadRequest)
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	switch state {
	case "", "open", "closed", "all":
		// ok
	default:
		http.Error(w, "state must be one of open|closed|all", http.StatusBadRequest)
		return "", forge.Remote{}, forge.ListOptions{}, false
	}

	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return "", forge.Remote{}, forge.ListOptions{}, false
	}
	_, remotes, err := s.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
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

func requireProjectRemoteID(w http.ResponseWriter, raw string) (string, bool) {
	remoteID := strings.TrimSpace(raw)
	if remoteID == "" {
		http.Error(w, "remoteId is required", http.StatusBadRequest)
		return "", false
	}
	return remoteID, true
}

// filterForUser keeps items whose author, assignees, or any of the
// extra user lists (e.g. requested reviewers) match login. The accessor
// funcs adapt the concrete PR/Issue shape to the common fields.
func filterForUser[T any](items []T, login string, author func(T) string, userLists func(T) [][]forge.User) []T {
	out := items[:0]
	for _, it := range items {
		if matchesUser(author(it), login) {
			out = append(out, it)
			continue
		}
		for _, list := range userLists(it) {
			if anyUserMatches(list, login) {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

// filterPRsForUser keeps PRs where login is author, an assignee, or
// a requested reviewer. Implements FR-5's "mine" semantics.
func filterPRsForUser(prs []forge.PR, login string) []forge.PR {
	return filterForUser(prs, login,
		func(p forge.PR) string { return p.Author },
		func(p forge.PR) [][]forge.User { return [][]forge.User{p.Assignees, p.RequestedReviewers} },
	)
}

// filterIssuesForUser keeps issues where login is author or assignee.
func filterIssuesForUser(issues []forge.Issue, login string) []forge.Issue {
	return filterForUser(issues, login,
		func(i forge.Issue) string { return i.Author },
		func(i forge.Issue) [][]forge.User { return [][]forge.User{i.Assignees} },
	)
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
