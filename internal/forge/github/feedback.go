package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
)

func (c *Client) Feedback(ctx context.Context, repo string, number int) ([]forge.Remark, error) {
	result := []forge.Remark{}
	for _, kind := range []string{"discussion", "inline", "review"} {
		path := fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number)
		if kind == "inline" {
			path = fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number)
		}
		if kind == "review" {
			path = fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
		}
		for page := 1; page <= 10; page++ {
			body, rl, status, err := c.fetch(ctx, path+"?per_page=100&page="+strconv.Itoa(page))
			if err != nil {
				return nil, err
			}
			if rl.Limited || rl.Stale || status != http.StatusOK {
				rl.Limited = rl.Limited || rl.Stale
				return nil, forgehttp.UpstreamStatus(status, rl)
			}
			var rows []struct {
				ID          int64  `json:"id"`
				Body        string `json:"body"`
				Path        string `json:"path"`
				URL         string `json:"html_url"`
				UpdatedAt   string `json:"updated_at"`
				CreatedAt   string `json:"created_at"`
				SubmittedAt string `json:"submitted_at"`
				User        struct {
					Login string `json:"login"`
				} `json:"user"`
			}
			if err := json.Unmarshal(body, &rows); err != nil {
				return nil, err
			}
			for _, r := range rows {
				at := r.UpdatedAt
				if at == "" {
					at = r.CreatedAt
				}
				if at == "" {
					at = r.SubmittedAt
				}
				if r.Body != "" {
					result = append(result, forge.Remark{ID: kind + ":" + strconv.FormatInt(r.ID, 10), Author: r.User.Login, Body: r.Body, Path: r.Path, URL: r.URL, UpdatedAt: at})
				}
			}
			if len(rows) < 100 {
				break
			}
			if page == 10 {
				return nil, fmt.Errorf("github feedback exceeds pagination limit")
			}
		}
	}
	return result, nil
}
