package forgejo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
)

func (c *Client) MergePR(ctx context.Context, repo string, number int, headSHA string) error {
	settings, rl, err := forgehttp.GetJSON[struct {
		DefaultMergeStyle string `json:"default_merge_style"`
	}](ctx, c.fetch, "/api/v1/repos/"+repo, "repository", forgehttp.UpstreamStatus)
	if err != nil {
		return err
	}
	if rl.Limited || rl.Stale {
		return forge.ErrRateLimited
	}
	switch settings.DefaultMergeStyle {
	case "merge", "rebase", "rebase-merge", "squash", "fast-forward-only":
	default:
		return fmt.Errorf("forgejo repository default merge style unavailable")
	}
	if headSHA == "" {
		return fmt.Errorf("expected PR head is required")
	}
	payload, err := json.Marshal(map[string]string{"Do": settings.DefaultMergeStyle, "head_commit_id": headSHA})
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/merge", repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	_, rl, status, err := forgehttp.Get(ctx, c.http, req)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return forgehttp.UpstreamStatus(status, rl)
	}
	return nil
}
