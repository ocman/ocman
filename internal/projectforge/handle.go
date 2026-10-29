package projectforge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// PRHeadFetchTimeout bounds a cross-fork head fetch on the hub and on the
// owner alike.
const PRHeadFetchTimeout = 30 * time.Second

// handleRequest is the body of POST /api/project/handle.
type handleRequest struct {
	Platform  string `json:"platform,omitempty"`
	Dir       string `json:"dir"`
	Remote    string `json:"remote"`
	RemoteID  string `json:"remoteId,omitempty"`
	Type      string `json:"type"`             // "pr" | "issue"
	Number    int    `json:"number"`           // PR or Issue number
	Mode      string `json:"mode"`             // "session" | "worktree"
	Action    string `json:"action,omitempty"` // "handle" (default) | "review" (pr only)
	FetchHead bool   `json:"fetchHead,omitempty"`
	Intent    string `json:"intent,omitempty"`
}

// handleHandle launches a new OpenCode session (or worktree-scoped session)
// on the explicit owner to "handle" a PR or issue.
//
//   - mode=session: render the template, create a session in dir.
//   - mode=worktree, issue: new branch "issue/<n>[-slug]" off the owner's
//     default base.
//   - mode=worktree, same-repo PR: attach the head branch.
//   - mode=worktree, cross-fork PR: 409 requires_fetch unless fetchHead,
//     which the user confirms; then the owner fetches it into ocman/pr-<n>.
func (m *Module) handleHandle(w http.ResponseWriter, r *http.Request) {
	var req handleRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host, ok := m.Owner(w, req.Dir, req.RemoteID)
	if !ok {
		return
	}
	repoRoot, remotes, err := m.detectUpstreams(r.Context(), host, req.Dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
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
	f, ok := m.forgeFor(w, rem)
	if !ok {
		return
	}
	// An unreadable item still launches, with a minimal prompt: the user
	// can recover inside the agent.
	prompt, vars, pr, err := m.renderPrompt(r.Context(), f, rem, req)
	if err != nil {
		log.WithError(err).Warn("handle: render prompt; falling back to minimal")
		prompt = markUntrusted(fallbackPrompt(req, rem, vars))
	}
	platform, _, err := m.CreatePlatform(r.Context(), host, req.Platform, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if m.Registered == nil || !m.Registered(platform) {
		http.Error(w, "platform not registered", http.StatusServiceUnavailable)
		return
	}
	if req.Mode == "session" {
		m.launchSession(w, r, req, host, platform, prompt, pr)
		return
	}
	m.launchWorktree(w, r, req, host, platform, rem, f, repoRoot, prompt, vars, pr)
}

// launchSession creates a session in the project directory, no checkout.
func (m *Module) launchSession(w http.ResponseWriter, r *http.Request, req handleRequest, host hostsvc.Host, platform, prompt string, pr *forge.PR) {
	port := ""
	_, base := platforms.SplitPlatformID(platform)
	ensured, err := host.EnsureRuntime(r.Context(), base, state.ProjectRootForDirectory(req.Dir))
	if err != nil && base != string(platforms.OpenCodeID) {
		http.Error(w, "launching runtime: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err == nil && ensured != nil {
		port = ensured.Port()
	}
	created, err := m.Sessions.Create(r.Context(), platform, platforms.CreateSessionRequest{Directory: req.Dir, Port: port})
	if err != nil {
		log.WithError(err).Warn("handle session: launch")
		http.Error(w, "launching session: "+err.Error(), http.StatusBadGateway)
		return
	}
	response := map[string]interface{}{"childSessionId": created.ID, "mode": "session", "platform": platform, "remoteId": host.RemoteID()}
	m.linkPR(r.Context(), req, platform, created.ID, req.Dir, pr)
	m.sendPrompt(r.Context(), platform, created.ID, prompt, response)
	httpx.WriteJSON(w, response)
}

// launchWorktree creates a worktree session on the owner; the branch
// depends on the item type and whether the PR comes from a fork.
func (m *Module) launchWorktree(
	w http.ResponseWriter, r *http.Request, req handleRequest, host hostsvc.Host, platform string,
	rem forge.Remote, f forge.Forge, repoRoot, prompt string, vars map[string]string, pr *forge.PR,
) {
	var branch string
	newBranch := false
	switch req.Type {
	case "issue":
		// BaseRef stays empty: the owner resolves its default base (AD-16),
		// so the hub never runs git for a remote project.
		branch, newBranch = issueBranchName(req.Number, vars["title"]), true
	case "pr":
		// Use the forge's PR, never the frontend's possibly stale row.
		if pr == nil {
			fetched, err := f.LookupPR(r.Context(), rem.Repo, req.Number)
			if err != nil {
				log.WithError(err).Warn("handle worktree: fetch pr metadata")
				http.Error(w, "failed to read PR metadata: "+err.Error(), http.StatusBadGateway)
				return
			}
			pr = &fetched
		}
		branch = pr.Branch
		if pr.CrossFork {
			var ok bool
			if branch, ok = m.fetchForkHead(w, r, req, host, repoRoot); !ok {
				return
			}
		}
	}
	_, base := platforms.SplitPlatformID(platform)
	res, err := host.CreateWorktreeSession(r.Context(), hostsvc.WorktreeSessionRequest{Platform: base, ProjectDir: repoRoot, Branch: branch, NewBranch: newBranch})
	if err != nil {
		log.WithError(err).Warn("handle worktree: launch")
		http.Error(w, "launching worktree session: "+err.Error(), http.StatusBadGateway)
		return
	}
	if res == nil || strings.TrimSpace(res.SessionID) == "" || !filepath.IsAbs(res.WorktreePath) || res.Branch != branch {
		log.Warn("handle worktree: owner returned no session")
		http.Error(w, "launching worktree session: owner returned no session", http.StatusBadGateway)
		return
	}
	response := map[string]interface{}{
		"childSessionId": res.SessionID, "mode": "worktree", "worktreePath": res.WorktreePath,
		"branch": res.Branch, "platform": platform, "remoteId": host.RemoteID(),
	}
	m.sendPrompt(r.Context(), platform, res.SessionID, prompt, response)
	m.linkPR(r.Context(), req, platform, res.SessionID, res.WorktreePath, pr)
	httpx.WriteJSON(w, response)
}

// fetchForkHead asks for confirmation (409 requires_fetch) until the user
// sent fetchHead, then has the owner fetch the PR head into ocman/pr-<n>.
func (m *Module) fetchForkHead(w http.ResponseWriter, r *http.Request, req handleRequest, host hostsvc.Host, repoRoot string) (string, bool) {
	expected := fmt.Sprintf("ocman/pr-%d", req.Number)
	if !req.FetchHead {
		httpx.WriteJSONStatus(w, http.StatusConflict, map[string]interface{}{
			"error": map[string]interface{}{
				"code":        "requires_fetch",
				"message":     "This PR is from a fork. Fetch pull/" + strconv.Itoa(req.Number) + "/head and create a worktree?",
				"fetchTarget": expected,
			},
		})
		return "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), PRHeadFetchTimeout)
	fetched, err := host.FetchPRHead(ctx, hostsvc.FetchPRHeadRequest{RepoRoot: repoRoot, Remote: req.Remote, Number: req.Number})
	cancel()
	if err != nil {
		log.Warn("handle worktree: fetch pr head failed")
		http.Error(w, "failed to fetch PR head", http.StatusBadGateway)
		return "", false
	}
	if fetched != expected {
		log.Warn("handle worktree: owner returned unexpected PR branch")
		http.Error(w, "failed to fetch PR head", http.StatusBadGateway)
		return "", false
	}
	return fetched, true
}

// sendPrompt delivers the first prompt; a failure keeps the created session
// and is reported as promptError.
func (m *Module) sendPrompt(ctx context.Context, platform, sessionID, prompt string, response map[string]interface{}) {
	if err := m.Sessions.SendMessage(ctx, platform, platforms.SendMessageRequest{SessionID: sessionID, Message: prompt}); err != nil {
		log.WithError(err).WithField("session", sessionID).Warn("handle: send prompt")
		response["promptError"] = "initial prompt was not sent"
	}
}

func (r handleRequest) validate() error {
	switch {
	case r.Dir == "":
		return errors.New("dir is required")
	case !filepath.IsAbs(r.Dir):
		return errors.New("dir must be an absolute path")
	case strings.TrimSpace(r.RemoteID) == "":
		return errors.New("remoteId is required")
	case r.Remote == "":
		return errors.New("remote is required")
	case r.Type != "pr" && r.Type != "issue":
		return errors.New("type must be 'pr' or 'issue'")
	case r.Number <= 0:
		return errors.New("number must be > 0")
	case r.Mode != "session" && r.Mode != "worktree":
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
