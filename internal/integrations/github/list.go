package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// HostName is the canonical GitHub host string. Used both by the forge
// adapter and by upstream-detection's host matching.
const HostName = "github.com"

// Host returns the canonical GitHub host. Implements forge.Forge.Host.
func (c *Client) Host() string { return HostName }

// Compile-time assertion: *Client satisfies forge.Forge.
var _ forge.Forge = (*Client)(nil)

// defaultPerPage is GitHub's typical page size cap that still fits
// comfortably in a sidebar render. The forge interface lets the
// handler ask for fewer; this is the value used when ListOptions.PerPage
// is zero.
const defaultPerPage = 30

// ListPRs returns one page of pull requests for owner/name.
// Implements forge.Forge.ListPRs.
//
// State mapping: GitHub returns state="open"|"closed" plus draft and
// merged_at; we collapse to ocman's "open"|"draft"|"merged"|"closed".
//
// Cross-fork detection: head.repo.full_name != base.repo.full_name.
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
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("per_page", strconv.Itoa(per))
	q.Set("page", strconv.Itoa(page))

	path := fmt.Sprintf("/repos/%s/pulls?%s", repo, q.Encode())

	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("github api %s: status %d", path, status)
	}

	var raw []ghPR
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding pulls: %w", err)
	}

	out := make([]forge.PR, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.toForge(repo))
	}
	return out, rl, nil
}

// ListIssues returns one page of issues for owner/name. GitHub's
// /issues endpoint returns both issues and PRs; the PR rows are
// filtered out here (they have a non-null pull_request field).
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
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("per_page", strconv.Itoa(per))
	q.Set("page", strconv.Itoa(page))

	path := fmt.Sprintf("/repos/%s/issues?%s", repo, q.Encode())

	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("github api %s: status %d", path, status)
	}

	var raw []ghIssue
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding issues: %w", err)
	}

	out := make([]forge.Issue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			// GitHub returns PRs in the issues endpoint; skip them
			// so callers see only real issues.
			continue
		}
		out = append(out, r.toForge(repo))
	}
	return out, rl, nil
}

// CurrentUser returns the authenticated user for the configured token.
// Returns forge.ErrUnauthenticated when the client has no token —
// /user always requires authentication.
//
// Implements forge.Forge.CurrentUser.
func (c *Client) CurrentUser(ctx context.Context) (forge.CurrentUser, error) {
	if c.token == "" {
		return forge.CurrentUser{}, forge.ErrUnauthenticated
	}

	body, _, status, err := c.fetch(ctx, "/user")
	if err != nil {
		return forge.CurrentUser{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return forge.CurrentUser{}, forge.ErrUnauthenticated
	}
	if status != http.StatusOK {
		return forge.CurrentUser{}, fmt.Errorf("github /user: status %d", status)
	}

	var raw struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return forge.CurrentUser{}, fmt.Errorf("decoding /user: %w", err)
	}
	return forge.CurrentUser{Host: HostName, Login: raw.Login}, nil
}

// fetch issues a GET request to path (relative to apiBase), reads the
// body, parses rate-limit headers, and returns body + rate-limit info
// + HTTP status. Network and parse errors are returned as err; an
// HTTP 429 is returned as status=429 with rl.Limited=true so callers
// can distinguish "rate limited" from "totally failed".
func (c *Client) fetch(ctx context.Context, path string) ([]byte, forge.RateLimit, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return nil, forge.RateLimit{}, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	httpClient := c.http
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, forge.RateLimit{}, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, forge.RateLimit{}, resp.StatusCode, err
	}
	rl := parseRateLimit(resp.Header, resp.StatusCode == http.StatusTooManyRequests)
	return body, rl, resp.StatusCode, nil
}

// parseRateLimit extracts Retry-After (seconds) or X-RateLimit-Reset
// (Unix seconds) from a response header. Returns Limited=false when
// neither header is present AND limited=false; otherwise Limited
// follows the limited flag and ResetAt is set when a header is parseable.
func parseRateLimit(h http.Header, limited bool) forge.RateLimit {
	if v := h.Get("Retry-After"); v != "" {
		// Retry-After can be HTTP-date or delta-seconds. We accept seconds.
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return forge.RateLimit{Limited: limited, ResetAt: time.Now().Add(time.Duration(secs) * time.Second)}
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return forge.RateLimit{Limited: limited, ResetAt: time.Unix(ts, 0)}
		}
	}
	return forge.RateLimit{Limited: limited}
}

// --- JSON shapes returned by GitHub's API ---

// ghPR is the subset of GitHub's pull-request JSON we read. The
// json field names match the API verbatim; everything we don't use
// is intentionally omitted so the type is grep-able for the actual
// dependencies on GitHub's shape.
type ghPR struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	State              string    `json:"state"`     // "open" | "closed"
	Draft              bool      `json:"draft"`
	MergedAt           *string   `json:"merged_at"` // non-nil => merged
	UpdatedAt          time.Time `json:"updated_at"`
	HTMLURL            string    `json:"html_url"`
	User               ghUser    `json:"user"`
	Labels             []ghLabel `json:"labels"`
	Assignees          []ghUser  `json:"assignees"`
	RequestedReviewers []ghUser  `json:"requested_reviewers"`
	Head               ghRef     `json:"head"`
	Base               ghRef     `json:"base"`
}

type ghIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
	HTMLURL     string    `json:"html_url"`
	User        ghUser    `json:"user"`
	Labels      []ghLabel `json:"labels"`
	Assignees   []ghUser  `json:"assignees"`
	PullRequest *struct{} `json:"pull_request,omitempty"` // present means this row is actually a PR
}

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type ghLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ghRef struct {
	Ref  string `json:"ref"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// toForge converts the raw GitHub shape into the forge-agnostic PR.
// repo is passed in (rather than read from base.repo.full_name) so
// rows missing repo info still come out with the right host/repo.
func (r ghPR) toForge(repo string) forge.PR {
	status := r.State
	switch {
	case r.State == "open" && r.Draft:
		status = "draft"
	case r.State == "closed" && r.MergedAt != nil:
		status = "merged"
	}
	pr := forge.PR{
		Number:    r.Number,
		Title:     r.Title,
		Body:     r.Body,
		Author:   r.User.Login,
		Status:    status,
		UpdatedAt: r.UpdatedAt,
		Branch:    r.Head.Ref,
		URL:       r.HTMLURL,
		Host:      HostName,
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

func (r ghIssue) toForge(repo string) forge.Issue {
	is := forge.Issue{
		Number:    r.Number,
		Title:     r.Title,
		Body:     r.Body,
		Author:   r.User.Login,
		Status:    r.State,
		UpdatedAt: r.UpdatedAt,
		URL:       r.HTMLURL,
		Host:      HostName,
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
