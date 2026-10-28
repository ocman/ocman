package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
)

var _ forge.IssueCreator = (*Client)(nil)

// CreateIssue opens an issue in owner/name.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string) (forge.Issue, error) {
	payload, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return forge.Issue{}, err
	}
	path := fmt.Sprintf("/repos/%s/issues", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, bytes.NewReader(payload))
	if err != nil {
		return forge.Issue{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, _, status, err := forgehttp.Get(ctx, c.http, req)
	if err != nil {
		return forge.Issue{}, err
	}
	if status != http.StatusCreated {
		return forge.Issue{}, fmt.Errorf("github api %s: status %d", path, status)
	}
	var issue forgehttp.Issue
	if err := json.Unmarshal(resp, &issue); err != nil {
		return forge.Issue{}, fmt.Errorf("decoding created issue: %w", err)
	}
	return issue.ToForge(HostName, repo), nil
}
