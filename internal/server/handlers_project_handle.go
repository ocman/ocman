package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/forge"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/tmux"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// handleProjectRequest is the JSON request body for POST /api/project/handle.
type handleProjectRequest struct {
	Dir       string `json:"dir"`
	Remote    string `json:"remote"`
	Type      string `json:"type"`             // "pr" | "issue"
	Number    int    `json:"number"`           // PR or Issue number
	Mode      string `json:"mode"`             // "session" | "worktree"
	Action    string `json:"action,omitempty"` // "handle" (default) | "review" (pr only)
	FetchHead bool   `json:"fetchHead,omitempty"`
	Intent    string `json:"intent,omitempty"`
}

// handleProjectHandle launches a new OpenCode session (or worktree-
// scoped session) to "handle" a PR or Issue. Reuses the existing
// mcp.SessionLauncher so child sessions land in state.db's
// child_sessions table the same way MCP-triggered launches do.
//
// POST /api/project/handle
// Localhost-only (it spawns tmux/opencode).
//
// Branching:
//   - mode=session: render template, call Launch in projectDir.
//   - mode=worktree: depends on item type:
//   - issue: NewBranch=true, branch="issue/<n>", BaseRef=default.
//   - PR (same-repo head): NewBranch=false, branch=<head ref>.
//   - PR (cross-fork, fetchHead=false): 409 requires_fetch.
//   - PR (cross-fork, fetchHead=true): FetchPRHead first, then
//     NewBranch=false with branch="ocman/pr-<n>".
func (s *Server) handleProjectHandle(w http.ResponseWriter, r *http.Request) {
	var req handleProjectRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve project remotes once so we can look up the target.
	repoRoot, remotes, err := s.detectUpstreams(r.Context(), req.Dir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "dir is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("handle: detect upstreams")
		http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		return
	}
	rem, ok := findRemote(remotes, req.Remote)
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

	// Render the prompt. The template + variables differ per item
	// type; if we can't fetch the item we still launch with a
	// minimal prompt (intent + url) rather than failing — the user
	// can always recover manually inside the agent.
	prompt, vars, err := s.renderHandlePrompt(r.Context(), f, rem, req)
	if err != nil {
		log.WithError(err).Warn("handle: render prompt; falling back to minimal")
		prompt = fallbackPrompt(req, rem, vars)
	}

	// Build the launcher lazily — same deps as the MCP server uses.
	launcher := s.newSessionLauncher()
	if launcher == nil {
		http.Error(w, "OpenCode platform not registered", http.StatusServiceUnavailable)
		return
	}

	switch req.Mode {
	case "session":
		s.handleProjectHandleSession(w, r, launcher, req, prompt)
	case "worktree":
		s.handleProjectHandleWorktree(w, r, launcher, req, rem, f, repoRoot, prompt, vars)
	default:
		http.Error(w, "mode must be 'session' or 'worktree'", http.StatusBadRequest)
	}
}

// handleProjectHandleSession is the FR-9 session branch: launches a
// new OpenCode session in the project directory, no branch checkout.
func (s *Server) handleProjectHandleSession(
	w http.ResponseWriter, r *http.Request,
	launcher *internalmcp.SessionLauncher,
	req handleProjectRequest, prompt string,
) {
	childID, err := launcher.Launch(r.Context(), internalmcp.LaunchRequest{
		// ParentSessionID intentionally empty: the launch was
		// initiated from the UI, not from a parent session. The
		// child_sessions schema allows an empty parent.
		Platform:       "opencode",
		Directory:      req.Dir,
		Intent:         req.intentOrDefault(),
		ComposedPrompt: prompt,
	})
	if err != nil {
		log.WithError(err).Warn("handle session: launch")
		http.Error(w, "launching child session: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{
		"childSessionId": childID,
		"mode":           "session",
	})
}

// handleProjectHandleWorktree is the FR-9 / FR-9a worktree branch.
// Branches by item type and cross-fork status.
func (s *Server) handleProjectHandleWorktree(
	w http.ResponseWriter, r *http.Request,
	launcher *internalmcp.SessionLauncher,
	req handleProjectRequest, rem forge.Remote, f forge.Forge,
	repoRoot, prompt string, vars map[string]string,
) {
	var branch string
	var newBranch bool
	var baseRef string

	switch req.Type {
	case "issue":
		// Brand-new branch off the default base ref, named
		// "issue/<n>-<slug-of-title>" if we have a title.
		branch = issueBranchName(req.Number, vars["title"])
		newBranch = true
		baseRef = worktree.ResolveBaseRef(r.Context(), repoRoot)

	case "pr":
		// Decide same-repo vs cross-fork. The PR row carries the
		// branch + crossFork flags in our normalized shape; refetch
		// the single PR here so we don't depend on the frontend
		// having sent stale info.
		pr, perr := s.fetchSinglePR(r.Context(), f, rem.Repo, req.Number)
		if perr != nil {
			log.WithError(perr).Warn("handle worktree: fetch pr metadata")
			http.Error(w, "failed to read PR metadata: "+perr.Error(), http.StatusBadGateway)
			return
		}
		if pr.CrossFork {
			if !req.FetchHead {
				// Tell the frontend to confirm + retry.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":        "requires_fetch",
						"message":     "This PR is from a fork. Fetch pull/" + strconv.Itoa(req.Number) + "/head and create a worktree?",
						"fetchTarget": fmt.Sprintf("ocman/pr-%d", req.Number),
					},
				})
				return
			}
			// Fetch the PR head into ocman/pr-<n> and attach.
			fetched, ferr := f.FetchPRHead(r.Context(), repoRoot, req.Remote, req.Number)
			if ferr != nil {
				log.WithError(ferr).Warn("handle worktree: fetch pr head")
				http.Error(w, "failed to fetch PR head: "+ferr.Error(), http.StatusBadGateway)
				return
			}
			branch = fetched
			newBranch = false
		} else {
			branch = pr.Branch
			newBranch = false
		}

	default:
		http.Error(w, "type must be 'pr' or 'issue'", http.StatusBadRequest)
		return
	}

	childID, wtResult, err := launcher.LaunchWithWorktree(
		r.Context(),
		internalmcp.LaunchRequest{
			Platform:       "opencode",
			Intent:         req.intentOrDefault(),
			ComposedPrompt: prompt,
		},
		worktree.CreateRequest{
			RepoRoot:  repoRoot,
			Branch:    branch,
			NewBranch: newBranch,
			BaseRef:   baseRef,
		},
	)
	if err != nil {
		log.WithError(err).Warn("handle worktree: launch")
		http.Error(w, "launching worktree session: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{
		"childSessionId": childID,
		"mode":           "worktree",
		"worktreePath":   wtResult.Path,
		"branch":         wtResult.Branch,
	})
}

// fetchSinglePR re-queries the forge for one PR by number. Uses the
// list endpoint with state=all + per-page=1 isn't safe; instead the
// frontend always provides Number and we trust it. To avoid a fresh
// API call, we list page-1 of "open" and find by number; if missing
// (e.g. closed PR), we list "all" once. This is slightly wasteful
// but keeps us off any "get single PR" endpoint that adapters don't
// expose. Returns ErrNotFound (a typed sentinel inside this file)
// when no PR matches.
func (s *Server) fetchSinglePR(ctx context.Context, f forge.Forge, repo string, number int) (forge.PR, error) {
	for _, state := range []string{"open", "all"} {
		prs, _, err := f.ListPRs(ctx, repo, forge.ListOptions{State: state, PerPage: 100})
		if err != nil {
			return forge.PR{}, err
		}
		for _, p := range prs {
			if p.Number == number {
				return p, nil
			}
		}
	}
	return forge.PR{}, errPRNotFound
}

var errPRNotFound = errors.New("pr not found")

// renderHandlePrompt fetches the PR/Issue and renders the configured
// template against it. Returns the rendered prompt plus the variables
// map so callers can reuse them (e.g. branch slug for issues).
func (s *Server) renderHandlePrompt(
	ctx context.Context, f forge.Forge, rem forge.Remote, req handleProjectRequest,
) (string, map[string]string, error) {
	tmpl, err := s.promptTemplateFor(req.Type, req.Action)
	if err != nil {
		return "", nil, err
	}

	vars := map[string]string{
		"number": strconv.Itoa(req.Number),
		"host":   rem.Host,
		"repo":   rem.Repo,
	}

	switch req.Type {
	case "pr":
		pr, perr := s.fetchSinglePR(ctx, f, rem.Repo, req.Number)
		if perr != nil {
			return "", vars, perr
		}
		vars["title"] = pr.Title
		vars["body"] = pr.Body
		vars["url"] = pr.URL
		vars["author"] = pr.Author
		vars["branch"] = pr.Branch
	case "issue":
		// No single-issue fetch helper — list and find. Mirrors
		// fetchSinglePR's two-pass approach.
		var found *forge.Issue
		for _, state := range []string{"open", "all"} {
			issues, _, ierr := f.ListIssues(ctx, rem.Repo, forge.ListOptions{State: state, PerPage: 100})
			if ierr != nil {
				return "", vars, ierr
			}
			for i := range issues {
				if issues[i].Number == req.Number {
					found = &issues[i]
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found == nil {
			return "", vars, errPRNotFound
		}
		vars["title"] = found.Title
		vars["body"] = found.Body
		vars["url"] = found.URL
		vars["author"] = found.Author
		// branch is intentionally absent for issues — template
		// placeholders for unknown keys stay literal per FR-10.
	}
	return forge.RenderPrompt(tmpl, vars), vars, nil
}

// promptTemplateFor returns the template for the given item type and
// action, falling back to the built-in defaults when state.db is
// unavailable or empty. action=="review" (PR only) selects the review
// template; anything else uses the type's handle template.
func (s *Server) promptTemplateFor(itemType, action string) (string, error) {
	def := DefaultPRPromptTemplate
	key := settingPRPromptTemplate
	switch {
	case itemType == "pr" && action == "review":
		def = DefaultReviewPromptTemplate
		key = settingReviewPromptTemplate
	case itemType == "issue":
		def = DefaultIssuePromptTemplate
		key = settingIssuePromptTemplate
	}
	if s.stateDB == nil {
		return def, nil
	}
	v, ok, err := s.stateDB.GetSetting(key)
	if err != nil {
		return def, err
	}
	if !ok {
		return def, nil
	}
	return v, nil
}

// fallbackPrompt builds a minimal usable prompt when template
// rendering / item fetch fails (network blip, item deleted, ...).
// Ensures the user-initiated launch still produces a session that's
// pointed at *something*.
func fallbackPrompt(req handleProjectRequest, rem forge.Remote, vars map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Please handle %s #%d on %s/%s.\n",
		req.Type, req.Number, rem.Host, rem.Repo)
	if title, ok := vars["title"]; ok && title != "" {
		fmt.Fprintf(&b, "Title: %s\n", title)
	}
	if url, ok := vars["url"]; ok && url != "" {
		fmt.Fprintf(&b, "URL: %s\n", url)
	}
	if req.Intent != "" {
		fmt.Fprintf(&b, "\n%s\n", req.Intent)
	}
	return b.String()
}

// issueBranchName produces the worktree branch name for an issue.
// Format: "issue/<n>" + "-<slug>" when title is non-empty. Slug is
// lowercase, [a-z0-9-] only, capped at 40 chars to keep the
// worktree path manageable.
func issueBranchName(number int, title string) string {
	base := fmt.Sprintf("issue/%d", number)
	if title == "" {
		return base
	}
	slug := slugifyIssueTitle(title)
	if slug == "" {
		return base
	}
	return base + "-" + slug
}

func slugifyIssueTitle(title string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// newSessionLauncher constructs a fresh SessionLauncher from the
// server's existing dependencies. Returns nil when the OpenCode
// platform isn't registered.
func (s *Server) newSessionLauncher() *internalmcp.SessionLauncher {
	if s.registry == nil {
		return nil
	}
	if _, ok := s.registry.Get(platforms.ID("opencode")); !ok {
		return nil
	}
	return internalmcp.NewSessionLauncher(
		s.stateDB,
		s.sessions.Client("opencode"),
		worktree.Create,
		internalmcp.TmuxLauncher(tmux.LaunchWorktreeWindow),
		internalmcp.PortDiscoverer(opencode.DiscoverOpenCodePortFresh),
	)
}

// --- request validation helpers ---

func (r handleProjectRequest) validate() error {
	if r.Dir == "" {
		return errors.New("dir is required")
	}
	if r.Remote == "" {
		return errors.New("remote is required")
	}
	switch r.Type {
	case "pr", "issue":
	default:
		return errors.New("type must be 'pr' or 'issue'")
	}
	if r.Number <= 0 {
		return errors.New("number must be > 0")
	}
	switch r.Mode {
	case "session", "worktree":
	default:
		return errors.New("mode must be 'session' or 'worktree'")
	}
	switch r.Action {
	case "", "handle":
	case "review":
		if r.Type != "pr" {
			return errors.New("action 'review' is only valid for type 'pr'")
		}
	default:
		return errors.New("action must be 'handle' or 'review'")
	}
	return nil
}

func (r handleProjectRequest) intentOrDefault() string {
	if r.Intent != "" {
		return r.Intent
	}
	verb := "handle"
	if r.Action == "review" {
		verb = "review"
	}
	return fmt.Sprintf("%s %s #%d", verb, r.Type, r.Number)
}
