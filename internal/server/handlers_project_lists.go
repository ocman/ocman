package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/git"
)

// projectForgeContext serves sidebar reads from the shared one-minute GitHub
// cache; only an explicit Refresh click (refresh=1) forces a fetch.
func projectForgeContext(r *http.Request) context.Context {
	if r.URL.Query().Get("refresh") == "1" {
		return github.WithRefresh(r.Context())
	}
	return github.WithCache(r.Context())
}

// Mine is a login resolved by the frontend per forge host, not a boolean.
func (s *Server) handleProjectPRs(w http.ResponseWriter, r *http.Request) {
	_, rem, opts, ok := s.parseProjectListParams(w, r)
	if !ok {
		return
	}
	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+rem.Host)
		return
	}
	prs, rl, err := f.ListPRs(projectForgeContext(r), rem.Repo, opts)
	if err != nil {
		writeProjectForgeError(w, r, rem, err)
		return
	}
	if writeProjectRateLimit(w, rl) {
		return
	}
	hasMore := !rl.Limited && len(prs) >= effectivePerPage(opts)
	if opts.Mine != "" {
		prs = filterPRsForUser(prs, opts.Mine)
	}
	writeForgeListResponse(w, "prs", prs, rl, opts, hasMore)
}

func (s *Server) handleProjectIssues(w http.ResponseWriter, r *http.Request) {
	_, rem, opts, ok := s.parseProjectListParams(w, r)
	if !ok {
		return
	}
	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+rem.Host)
		return
	}
	issues, rl, err := f.ListIssues(projectForgeContext(r), rem.Repo, opts)
	if err != nil {
		writeProjectForgeError(w, r, rem, err)
		return
	}
	if writeProjectRateLimit(w, rl) {
		return
	}
	hasMore := !rl.Limited && len(issues) >= effectivePerPage(opts)
	if opts.Mine != "" {
		issues = filterIssuesForUser(issues, opts.Mine)
	}
	writeForgeListResponse(w, "issues", issues, rl, opts, hasMore)
}

func writeForgeListResponse[T any](w http.ResponseWriter, key string, items []T, rl forge.RateLimit, opts forge.ListOptions, hasMore bool) {
	writeJSON(w, map[string]interface{}{
		key: items, "pagination": map[string]interface{}{"page": opts.Page, "hasMore": hasMore}, "rateLimit": rl,
	})
}

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
		if writeCancellation(w, "failed to detect upstreams", err) {
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
		writeProjectListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+rem.Host)
		return
	}
	u, err := f.CurrentUser(projectForgeContext(r))
	if err != nil {
		if errors.Is(err, forge.ErrUnauthenticated) {
			writeProjectListError(w, http.StatusUnauthorized, "auth_required", "not authenticated")
			return
		}
		writeProjectForgeError(w, r, rem, err)
		return
	}
	writeJSON(w, u)
}

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
		if writeCancellation(w, "failed to detect upstreams", err) {
			return "", forge.Remote{}, forge.ListOptions{}, false
		}
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
	return dir, rem, forge.ListOptions{State: state, Mine: mine, Page: page}, true
}
