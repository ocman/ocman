package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/forge/forgehttp"
)

func (c *Client) MergePR(ctx context.Context, repo string, number int, headSHA string) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || headSHA == "" {
		return fmt.Errorf("repository and expected PR head are required")
	}
	payload, err := json.Marshal(map[string]any{
		"query":     "query($owner:String!,$name:String!){repository(owner:$owner,name:$name){viewerDefaultMergeMethod}}",
		"variables": map[string]string{"owner": owner, "name": name},
	})
	if err != nil {
		return err
	}
	body, err := c.mergeRequest(ctx, http.MethodPost, "/graphql", payload)
	if err != nil {
		return err
	}
	var result struct {
		Data struct {
			Repository struct{ ViewerDefaultMergeMethod string }
		}
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	method := strings.ToLower(result.Data.Repository.ViewerDefaultMergeMethod)
	if len(result.Errors) != 0 || (method != "merge" && method != "squash" && method != "rebase") {
		return fmt.Errorf("github repository default merge method unavailable")
	}
	payload, err = json.Marshal(map[string]string{"merge_method": method, "sha": headSHA})
	if err != nil {
		return err
	}
	body, err = c.mergeRequest(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number), payload)
	if err != nil {
		return err
	}
	var merged struct{ Merged bool }
	if err := json.Unmarshal(body, &merged); err != nil {
		return err
	}
	if !merged.Merged {
		return fmt.Errorf("github did not merge the pull request")
	}
	return nil
}

func (c *Client) mergeRequest(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	body, rl, status, err := forgehttp.Get(ctx, c.http, req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, forgehttp.UpstreamStatus(status, rl)
	}
	return body, nil
}
