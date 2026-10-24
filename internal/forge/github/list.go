package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
)

// HostName is the canonical GitHub host string. Used both by the forge
// adapter and by upstream-detection's host matching.
const HostName = "github.com"

// Host returns the canonical GitHub host. Implements forge.Forge.Host.
func (c *Client) Host() string { return HostName }

// Compile-time assertion: *Client satisfies forge.Forge.
var _ forge.Forge = (*Client)(nil)

// ListPRs returns one page of pull requests for owner/name.
// Implements forge.Forge.ListPRs.
//
// State mapping: GitHub returns state="open"|"closed" plus draft and
// merged_at; we collapse to ocman's "open"|"draft"|"merged"|"closed".
//
// Cross-fork detection: head.repo.full_name != base.repo.full_name.
func (c *Client) ListPRs(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.PR, forge.RateLimit, error) {
	path := fmt.Sprintf("/repos/%s/pulls?%s", repo, listQuery(opts))
	return forgehttp.List(ctx, c.fetch, path, "pulls", func(r forgehttp.PR) (forge.PR, bool) {
		return toPR(r, repo), true
	})
}

func (c *Client) LookupPR(ctx context.Context, repo string, number int) (forge.PR, error) {
	raw, _, err := forgehttp.GetJSON[forgehttp.PR](ctx, c.fetch, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), "pull", forgehttp.UpstreamStatus)
	if err != nil {
		return forge.PR{}, err
	}
	return toPR(raw, repo), nil
}

func (c *Client) PRApproval(ctx context.Context, repo string, number int) (bool, error) {
	return forgehttp.ReviewApproval(ctx, c.fetch, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), "per_page")
}

func (c *Client) ConvertPRToDraft(ctx context.Context, repo string, number int) error {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	body, _, status, err := c.fetch(ctx, path)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("github api %s: status %d", path, status)
	}
	var pr struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return fmt.Errorf("decoding pull: %w", err)
	}
	if pr.NodeID == "" {
		return errors.New("github pull request has no node ID")
	}
	payload, err := json.Marshal(map[string]any{
		"query":     "mutation($id:ID!){convertPullRequestToDraft(input:{pullRequestId:$id}){pullRequest{isDraft}}}",
		"variables": map[string]string{"id": pr.NodeID},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	body, _, status, err = forgehttp.Get(ctx, c.http, req)
	if err != nil {
		return err
	}
	var result struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if status != http.StatusOK {
		return fmt.Errorf("github graphql: status %d", status)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decoding github graphql response: %w", err)
	}
	if len(result.Errors) != 0 {
		return fmt.Errorf("github graphql: %s", result.Errors[0].Message)
	}
	return nil
}

// ListIssues returns one page of issues for owner/name. GitHub's
// /issues endpoint returns both issues and PRs; the PR rows are
// filtered out here (they have a non-null pull_request field).
//
// Implements forge.Forge.ListIssues.
func (c *Client) ListIssues(ctx context.Context, repo string, opts forge.ListOptions) ([]forge.Issue, forge.RateLimit, error) {
	path := fmt.Sprintf("/repos/%s/issues?%s", repo, listQuery(opts))
	return forgehttp.List(ctx, c.fetch, path, "issues", func(r forgehttp.Issue) (forge.Issue, bool) {
		return r.ToForge(HostName, repo), r.PullRequest == nil
	})
}

func (c *Client) LookupIssue(ctx context.Context, repo string, number int) (forge.Issue, error) {
	raw, _, err := forgehttp.GetJSON[forgehttp.Issue](ctx, c.fetch, fmt.Sprintf("/repos/%s/issues/%d", repo, number), "issue", forgehttp.UpstreamStatus)
	if err != nil {
		return forge.Issue{}, err
	}
	if raw.PullRequest != nil {
		return forge.Issue{}, fmt.Errorf("github issue %d is a pull request", number)
	}
	return raw.ToForge(HostName, repo), nil
}

// CurrentUser returns the authenticated user for the configured token.
// Returns forge.ErrUnauthenticated when the client has no token —
// /user always requires authentication.
//
// Implements forge.Forge.CurrentUser.
func (c *Client) CurrentUser(ctx context.Context) (forge.CurrentUser, error) {
	return forgehttp.CurrentUser(ctx, c.fetch, HostName, "/user", c.token)
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
	return forgehttp.Get(ctx, c.http, req)
}

func listQuery(opts forge.ListOptions) string {
	opts = opts.WithDefaults()
	q := url.Values{}
	q.Set("state", opts.State)
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("per_page", strconv.Itoa(opts.PerPage))
	q.Set("page", strconv.Itoa(opts.Page))
	return q.Encode()
}

func toPR(r forgehttp.PR, repo string) forge.PR {
	return r.ToForge(HostName, repo, r.MergedAt != nil)
}
