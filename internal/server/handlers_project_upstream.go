package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
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
		if writeCancellation(w, "failed to detect upstreams", err) {
			return
		}
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
