// Package forgejo provides a thin Forgejo/Gitea REST API client and a
// per-host Registry that maps hostnames to authenticated clients.
//
// Token discovery order (FR-11, env-first):
//  1. FORGEJO_TOKEN environment variable (applies to all hosts)
//  2. GITEA_TOKEN environment variable   (applies to all hosts)
//  3. token from `tea`'s ~/.config/tea/config.yml (per-host)
//
// Host URL discovery is independent of the token: hosts always come
// from `tea`'s config (an env-var token carries no host information),
// per AD-3 in spec/pr-issue-sidebar/architecture.md.
package forgejo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/integrations/forgehttp"
)

// defaultPerPage matches the GitHub adapter — small enough to keep
// the sidebar payload bounded.
const defaultPerPage = 30

// Client is a Forgejo REST API client bound to a single host.
type Client struct {
	// baseURL is the Forgejo root URL WITHOUT a trailing slash.
	// Endpoints are appended as "/api/v1/...".
	baseURL string
	// host is the parsed host of baseURL (e.g. "code.example.com").
	// Stored alongside baseURL so callers don't have to re-parse.
	host  string
	token string
	http  *http.Client
}

// NewClient builds a Client for a Forgejo host. envToken, when non-empty,
// overrides any token configured in tea for this host (FR-11, env-first).
func NewClient(host, baseURL, teaToken string) *Client {
	envToken := envTokenForHost()
	tok := envToken
	if tok == "" {
		tok = teaToken
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		host:    host,
		token:   tok,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// envTokenForHost returns the env-var token if set. The env vars apply
// to every Forgejo host (FORGEJO_TOKEN / GITEA_TOKEN are not
// host-scoped); the caller decides whether to use it.
func envTokenForHost() string {
	if t := os.Getenv("FORGEJO_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITEA_TOKEN")
}

// Host returns the canonical hostname. Implements forge.Forge.Host.
func (c *Client) Host() string { return c.host }

// Authenticated reports whether a token is configured.
func (c *Client) Authenticated() bool { return c.token != "" }

// ListPRs returns one page of pull requests for owner/name.
// Implements forge.Forge.ListPRs.
func (c *Client) ListPRs(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.PR, forge.RateLimit, error) {
	state := opts.State
	if state == "" {
		state = "open"
	}
	page := opts.Page
	if page < 1 {
		page = 1
	}
	per := opts.PerPage
	if per <= 0 {
		per = defaultPerPage
	}
	q := url.Values{}
	q.Set("state", state)
	q.Set("sort", "newest")
	q.Set("limit", strconv.Itoa(per))
	q.Set("page", strconv.Itoa(page))

	path := fmt.Sprintf("/api/v1/repos/%s/pulls?%s", repo, q.Encode())
	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("forgejo %s: status %d", path, status)
	}

	var raw []fjPR
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding pulls: %w", err)
	}
	out := make([]forge.PR, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toForge(c.host, repo))
	}
	return out, rl, nil
}

// ListIssues returns one page of issues for owner/name. Uses
// type=issues to ask Forgejo to scope the result; rows with
// pull_request set are also skipped defensively.
//
// Implements forge.Forge.ListIssues.
func (c *Client) ListIssues(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.Issue, forge.RateLimit, error) {
	state := opts.State
	if state == "" {
		state = "open"
	}
	page := opts.Page
	if page < 1 {
		page = 1
	}
	per := opts.PerPage
	if per <= 0 {
		per = defaultPerPage
	}
	q := url.Values{}
	q.Set("state", state)
	q.Set("type", "issues")
	q.Set("limit", strconv.Itoa(per))
	q.Set("page", strconv.Itoa(page))

	path := fmt.Sprintf("/api/v1/repos/%s/issues?%s", repo, q.Encode())
	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("forgejo %s: status %d", path, status)
	}

	var raw []fjIssue
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding issues: %w", err)
	}
	out := make([]forge.Issue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		out = append(out, r.toForge(c.host, repo))
	}
	return out, rl, nil
}

// CurrentUser returns the authenticated user. Returns
// forge.ErrUnauthenticated when the client has no token.
// Implements forge.Forge.CurrentUser.
func (c *Client) CurrentUser(ctx context.Context) (forge.CurrentUser, error) {
	if c.token == "" {
		return forge.CurrentUser{}, forge.ErrUnauthenticated
	}
	body, _, status, err := c.fetch(ctx, "/api/v1/user")
	if err != nil {
		return forge.CurrentUser{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return forge.CurrentUser{}, forge.ErrUnauthenticated
	}
	if status != http.StatusOK {
		return forge.CurrentUser{}, fmt.Errorf("forgejo /user: status %d", status)
	}
	var raw struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return forge.CurrentUser{}, fmt.Errorf("decoding /user: %w", err)
	}
	return forge.CurrentUser{Host: c.host, Login: raw.Login}, nil
}

// FetchPRHead fetches the PR head ref into a deterministic local
// branch "ocman/pr-<n>". Forgejo's PR refs follow the same
// refs/pull/<n>/head convention as GitHub, so the implementation
// is identical.
//
// Implements forge.Forge.FetchPRHead.
func (c *Client) FetchPRHead(ctx context.Context, repoRoot, remoteName string, prNumber int) (string, error) {
	if repoRoot == "" || remoteName == "" || prNumber <= 0 {
		return "", fmt.Errorf("forgejo: FetchPRHead requires repoRoot, remoteName, prNumber > 0")
	}
	branch := fmt.Sprintf("ocman/pr-%d", prNumber)
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", prNumber, branch)
	cmd := gitexec.Command(ctx, "-C", repoRoot, "fetch", remoteName, refspec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git fetch %s %s: %w: %s",
			remoteName, refspec, err, strings.TrimSpace(string(out)))
	}
	return branch, nil
}

// fetch issues a GET against c.baseURL + path. Identical to the
// github client's helper, except it uses Forgejo's "token <t>" auth
// scheme (matches what tea writes and what gitea/forgejo accept).
func (c *Client) fetch(ctx context.Context, path string) ([]byte, forge.RateLimit, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, forge.RateLimit{}, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		// Forgejo/Gitea use the "token <t>" scheme (matches what tea
		// writes), unlike GitHub's "Bearer <t>".
		req.Header.Set("Authorization", "token "+c.token)
	}
	return forgehttp.Get(ctx, c.http, req)
}

// --- JSON shapes ---
//
// Forgejo's API is Gitea-compatible. The fields below cover what the
// sidebar needs; anything we don't read is intentionally omitted.

type fjPR struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	State              string    `json:"state"`
	Draft              bool      `json:"draft"`
	Merged             bool      `json:"merged"`
	UpdatedAt          time.Time `json:"updated_at"`
	HTMLURL            string    `json:"html_url"`
	User               fjUser    `json:"user"`
	Labels             []fjLabel `json:"labels"`
	Assignees          []fjUser  `json:"assignees"`
	RequestedReviewers []fjUser  `json:"requested_reviewers"`
	Head               fjRef     `json:"head"`
	Base               fjRef     `json:"base"`
}

type fjIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
	HTMLURL     string    `json:"html_url"`
	User        fjUser    `json:"user"`
	Labels      []fjLabel `json:"labels"`
	Assignees   []fjUser  `json:"assignees"`
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

type fjUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type fjLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type fjRef struct {
	Ref  string `json:"ref"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

func (r fjPR) toForge(host, repo string) forge.PR {
	status := r.State
	switch {
	case r.State == "open" && r.Draft:
		status = "draft"
	case r.State == "closed" && r.Merged:
		status = "merged"
	}
	pr := forge.PR{
		Number:    r.Number,
		Title:     r.Title,
		Body:      r.Body,
		Author:    r.User.Login,
		Status:    status,
		UpdatedAt: r.UpdatedAt,
		Branch:    r.Head.Ref,
		URL:       r.HTMLURL,
		Host:      host,
		Repo:      repo,
		CrossFork: r.Head.Repo.FullName != "" && r.Head.Repo.FullName != r.Base.Repo.FullName,
	}
	for _, l := range r.Labels {
		pr.Labels = append(pr.Labels, forge.Label{Name: l.Name, Color: l.Color})
	}
	for _, u := range r.Assignees {
		pr.Assignees = append(pr.Assignees, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	for _, u := range r.RequestedReviewers {
		pr.RequestedReviewers = append(pr.RequestedReviewers, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	return pr
}

func (r fjIssue) toForge(host, repo string) forge.Issue {
	is := forge.Issue{
		Number:    r.Number,
		Title:     r.Title,
		Body:      r.Body,
		Author:    r.User.Login,
		Status:    r.State,
		UpdatedAt: r.UpdatedAt,
		URL:       r.HTMLURL,
		Host:      host,
		Repo:      repo,
	}
	for _, l := range r.Labels {
		is.Labels = append(is.Labels, forge.Label{Name: l.Name, Color: l.Color})
	}
	for _, u := range r.Assignees {
		is.Assignees = append(is.Assignees, forge.User{Login: u.Login, AvatarURL: u.AvatarURL})
	}
	return is
}

// Compile-time assertion: *Client satisfies forge.Forge.
var _ forge.Forge = (*Client)(nil)
