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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
)

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
		http:    forgehttp.InstrumentClient(nil),
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

// NewForTest returns a Client wired against the given host, baseURL and
// token, using the supplied http.Client. Intended for tests that point the
// client at an httptest.Server; not used in production paths.
func NewForTest(host, baseURL, token string, httpClient *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		host:    host,
		token:   token,
		http:    forgehttp.InstrumentClient(httpClient),
	}
}

// Host returns the canonical hostname. Implements forge.Forge.Host.
func (c *Client) Host() string { return c.host }

// Authenticated reports whether a token is configured.
func (c *Client) Authenticated() bool { return c.token != "" }

// Token is the owner machine's env/tea token ("" when anonymous).
func (c *Client) Token() string { return c.token }

// ListPRs returns one page of pull requests for owner/name.
// Implements forge.Forge.ListPRs.
func (c *Client) ListPRs(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.PR, forge.RateLimit, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/pulls?%s", repo, listQuery(opts, "sort", "newest"))
	return forgehttp.List(ctx, c.fetch, path, "pulls", func(r forgehttp.PR) (forge.PR, bool) {
		return toPR(r, c.host, repo), true
	})
}

func (c *Client) LookupPR(ctx context.Context, repo string, number int) (forge.PR, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo, number)
	raw, _, err := forgehttp.GetJSON[forgehttp.PR](ctx, c.fetch, path, "pull", statusErr(path))
	if err != nil {
		return forge.PR{}, err
	}
	return toPR(raw, c.host, repo), nil
}

func (c *Client) PRApproval(ctx context.Context, repo string, number int) (bool, error) {
	return forgehttp.ReviewApproval(ctx, c.fetch, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", repo, number), "limit")
}

func (c *Client) ConvertPRToDraft(ctx context.Context, repo string, number int) error {
	current, err := c.LookupPR(ctx, repo, number)
	if err != nil {
		return err
	}
	if current.Status == "draft" {
		return nil
	}
	// Forgejo derives draft status from the title, not an editable draft flag.
	payload, err := json.Marshal(map[string]string{"title": "WIP: " + current.Title})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	body, _, status, err := forgehttp.Get(ctx, c.http, req)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("forgejo %s: status %d", path, status)
	}
	var pr forgehttp.PR
	if err := json.Unmarshal(body, &pr); err != nil {
		return fmt.Errorf("decoding edited pull: %w", err)
	}
	if !pr.Draft {
		return errors.New("forgejo pull request was not converted to draft")
	}
	return nil
}

// ListIssues returns one page of issues for owner/name. Uses
// type=issues to ask Forgejo to scope the result; rows with
// pull_request set are also skipped defensively.
//
// Implements forge.Forge.ListIssues.
func (c *Client) ListIssues(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.Issue, forge.RateLimit, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/issues?%s", repo, listQuery(opts, "type", "issues"))
	return forgehttp.List(ctx, c.fetch, path, "issues", func(r forgehttp.Issue) (forge.Issue, bool) {
		return r.ToForge(c.host, repo), r.PullRequest == nil
	})
}

func (c *Client) LookupIssue(ctx context.Context, repo string, number int) (forge.Issue, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/issues/%d", repo, number)
	raw, _, err := forgehttp.GetJSON[forgehttp.Issue](ctx, c.fetch, path, "issue", statusErr(path))
	if err != nil {
		return forge.Issue{}, err
	}
	if raw.PullRequest != nil {
		return forge.Issue{}, fmt.Errorf("forgejo issue %d is a pull request", number)
	}
	return raw.ToForge(c.host, repo), nil
}

// CurrentUser returns the authenticated user. Returns
// forge.ErrUnauthenticated when the client has no token.
// Implements forge.Forge.CurrentUser.
func (c *Client) CurrentUser(ctx context.Context) (forge.CurrentUser, error) {
	return forgehttp.CurrentUser(ctx, c.fetch, c.host, "/api/v1/user", c.token)
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

// BaseURL returns the Forgejo root URL (without a trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// Compile-time assertion: *Client satisfies forge.Forge.
var _ forge.Forge = (*Client)(nil)
