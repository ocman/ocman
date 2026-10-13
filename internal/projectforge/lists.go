package projectforge

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// forgeContext serves sidebar reads from the shared one-minute GitHub
// cache; only an explicit Refresh click (refresh=1) forces a fetch.
func forgeContext(r *http.Request) context.Context {
	if r.URL.Query().Get("refresh") == "1" {
		return github.WithRefresh(r.Context())
	}
	return github.WithCache(r.Context())
}

// Mine is a login resolved by the frontend per forge host, not a boolean.
func (m *Module) handlePRs(w http.ResponseWriter, r *http.Request) {
	rem, f, opts, ok := m.listTarget(w, r)
	if !ok {
		return
	}
	prs, rl, err := f.ListPRs(forgeContext(r), rem.Repo, opts)
	if err != nil {
		writeForgeError(w, r, rem, err)
		return
	}
	if writeRateLimit(w, rl) {
		return
	}
	hasMore := !rl.Limited && len(prs) >= effectivePerPage(opts)
	if opts.Mine != "" {
		prs = filterPRsForUser(prs, opts.Mine)
	}
	writeListResponse(w, "prs", prs, rl, opts, hasMore)
}

func (m *Module) handleIssues(w http.ResponseWriter, r *http.Request) {
	rem, f, opts, ok := m.listTarget(w, r)
	if !ok {
		return
	}
	issues, rl, err := f.ListIssues(forgeContext(r), rem.Repo, opts)
	if err != nil {
		writeForgeError(w, r, rem, err)
		return
	}
	if writeRateLimit(w, rl) {
		return
	}
	hasMore := !rl.Limited && len(issues) >= effectivePerPage(opts)
	if opts.Mine != "" {
		issues = filterIssuesForUser(issues, opts.Mine)
	}
	writeListResponse(w, "issues", issues, rl, opts, hasMore)
}

func (m *Module) handlePRMergeability(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.URL.Query().Get("number"))
	if err != nil || number <= 0 {
		http.Error(w, "positive PR number is required", http.StatusBadRequest)
		return
	}
	rem, f, _, ok := m.listTarget(w, r)
	if !ok {
		return
	}
	ctx := forgeContext(r)
	pr, err := f.LookupPR(ctx, rem.Repo, number)
	if err != nil {
		writeForgeError(w, r, rem, err)
		return
	}
	approved, err := f.PRApproval(ctx, rem.Repo, number)
	if err != nil {
		writeForgeError(w, r, rem, err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"mergeable": pr.Mergeable, "approved": approved})
}

// handlePRChecks returns CI status for a PR's head commit.
// GET /api/project/pr-checks?dir=<abs>&remote=<name>&remoteId=<owner>&sha=<headSha>
func (m *Module) handlePRChecks(w http.ResponseWriter, r *http.Request) {
	dir, ok := httpx.AbsDir(w, r)
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
	rem, ok := m.ownerRemote(w, r, dir, remoteName, true)
	if !ok {
		return
	}
	f, ok := m.forgeFor(w, rem)
	if !ok {
		return
	}
	ci, rl, err := f.Checks(forgeContext(r), rem.Repo, sha)
	if err != nil {
		writeForgeError(w, r, rem, err)
		return
	}
	if writeRateLimit(w, rl) {
		return
	}
	if ci.Checks == nil {
		ci.Checks = []forge.Check{}
	}
	httpx.WriteJSON(w, map[string]interface{}{"state": ci.State, "checks": ci.Checks, "rateLimit": rl})
}

func (m *Module) handleForgeUser(w http.ResponseWriter, r *http.Request) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return
	}
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	if remoteName == "" {
		http.Error(w, "remote is required", http.StatusBadRequest)
		return
	}
	// Identity reads report every detection failure as 502, never 404.
	rem, ok := m.ownerRemote(w, r, dir, remoteName, false)
	if !ok {
		return
	}
	f, ok := m.forgeFor(w, rem)
	if !ok {
		return
	}
	u, err := f.CurrentUser(forgeContext(r))
	if err != nil {
		if errors.Is(err, forge.ErrUnauthenticated) {
			writeListError(w, http.StatusUnauthorized, "auth_required", "not authenticated")
			return
		}
		writeForgeError(w, r, rem, err)
		return
	}
	httpx.WriteJSON(w, u)
}

// listTarget parses the shared list parameters, resolves the explicit owner
// and its named upstream, and the hub's client for it.
func (m *Module) listTarget(w http.ResponseWriter, r *http.Request) (forge.Remote, forge.Forge, forge.ListOptions, bool) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return forge.Remote{}, nil, forge.ListOptions{}, false
	}
	q := r.URL.Query()
	remoteName := strings.TrimSpace(q.Get("remote"))
	state := strings.TrimSpace(q.Get("state"))
	mine := strings.TrimSpace(q.Get("mine"))
	page, _ := strconv.Atoi(q.Get("page"))
	if remoteName == "" {
		http.Error(w, "remote query parameter is required", http.StatusBadRequest)
		return forge.Remote{}, nil, forge.ListOptions{}, false
	}
	switch state {
	case "", "open", "closed", "all":
	default:
		http.Error(w, "state must be one of open|closed|all", http.StatusBadRequest)
		return forge.Remote{}, nil, forge.ListOptions{}, false
	}
	rem, ok := m.ownerRemote(w, r, dir, remoteName, true)
	if !ok {
		return forge.Remote{}, nil, forge.ListOptions{}, false
	}
	f, ok := m.forgeFor(w, rem)
	if !ok {
		return forge.Remote{}, nil, forge.ListOptions{}, false
	}
	return rem, f, forge.ListOptions{State: state, Mine: mine, Page: page}, true
}

// ownerRemote resolves the required explicit owner and finds remoteName
// among its upstreams. notARepo404 maps a non-repository to 404 rather
// than 502.
func (m *Module) ownerRemote(w http.ResponseWriter, r *http.Request, dir, remoteName string, notARepo404 bool) (forge.Remote, bool) {
	remoteID, ok := httpx.RemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return forge.Remote{}, false
	}
	host, ok := m.Owner(w, dir, remoteID)
	if !ok {
		return forge.Remote{}, false
	}
	_, remotes, err := m.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if httpx.WriteCancellation(w, "failed to detect upstreams", err) {
			return forge.Remote{}, false
		}
		if notARepo404 && errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
		} else {
			http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		}
		return forge.Remote{}, false
	}
	rem, ok := findRemote(remotes, remoteName)
	if !ok {
		http.Error(w, "remote not found among project upstreams", http.StatusNotFound)
		return forge.Remote{}, false
	}
	return rem, true
}

func (m *Module) forgeFor(w http.ResponseWriter, rem forge.Remote) (forge.Forge, bool) {
	f, ok := m.resolveForge(rem)
	if !ok {
		writeListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+rem.Host)
	}
	return f, ok
}

func writeListResponse[T any](w http.ResponseWriter, key string, items []T, rl forge.RateLimit, opts forge.ListOptions, hasMore bool) {
	httpx.WriteJSON(w, map[string]interface{}{
		key: items, "pagination": map[string]interface{}{"page": opts.Page, "hasMore": hasMore}, "rateLimit": rl,
	})
}

func validCommitSHA(sha string) bool {
	if len(sha) < 3 || len(sha) > 64 {
		return false
	}
	for _, r := range sha {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// effectivePerPage mirrors the adapters' default page size (30), used by
// the hasMore heuristic.
func effectivePerPage(opts forge.ListOptions) int {
	if opts.PerPage > 0 {
		return opts.PerPage
	}
	return 30
}

// filterForUser keeps items whose author, assignees, or any of the extra
// user lists (e.g. requested reviewers) match login.
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

// filterPRsForUser keeps PRs where login is author, an assignee, or a
// requested reviewer (FR-5's "mine").
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
