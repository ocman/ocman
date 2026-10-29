package forgejo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ocman/ocman/internal/forge"
)

func (c *Client) Feedback(ctx context.Context, repo string, number int) ([]forge.Remark, error) {
	result := []forge.Remark{}
	for _, kind := range []string{"discussion", "review"} {
		path := fmt.Sprintf("/api/v1/repos/%s/issues/%d/comments", repo, number)
		if kind == "review" {
			path = fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews", repo, number)
		}
		for page := 1; page <= 10; page++ {
			body, rl, status, err := c.fetch(ctx, path+"?limit=100&page="+strconv.Itoa(page))
			if err != nil {
				return nil, err
			}
			if rl.Limited || status != http.StatusOK {
				return nil, fmt.Errorf("forgejo feedback unavailable: status %d", status)
			}
			var rows []struct {
				ID          int64  `json:"id"`
				Body        string `json:"body"`
				URL         string `json:"html_url"`
				UpdatedAt   string `json:"updated_at"`
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
					at = r.SubmittedAt
				}
				if r.Body != "" {
					result = append(result, forge.Remark{ID: kind + ":" + strconv.FormatInt(r.ID, 10), Author: r.User.Login, Body: r.Body, URL: r.URL, UpdatedAt: at})
				}
				if kind == "review" {
					comments, err := c.reviewComments(ctx, repo, number, r.ID)
					if err != nil {
						return nil, err
					}
					result = append(result, comments...)
				}
			}
			if len(rows) < 100 {
				break
			}
			if page == 10 {
				return nil, fmt.Errorf("forgejo feedback exceeds pagination limit")
			}
		}
	}
	return result, nil
}

func (c *Client) reviewComments(ctx context.Context, repo string, number int, review int64) ([]forge.Remark, error) {
	result := []forge.Remark{}
	for page := 1; page <= 10; page++ {
		path := fmt.Sprintf("/api/v1/repos/%s/pulls/%d/reviews/%d/comments?limit=100&page=%d", repo, number, review, page)
		body, rl, status, err := c.fetch(ctx, path)
		if err != nil {
			return nil, err
		}
		if rl.Limited || status != http.StatusOK {
			return nil, fmt.Errorf("forgejo review comments unavailable: status %d", status)
		}
		var rows []struct {
			ID        int64  `json:"id"`
			Body      string `json:"body"`
			Path      string `json:"path"`
			URL       string `json:"html_url"`
			UpdatedAt string `json:"updated_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			result = append(result, forge.Remark{ID: "inline:" + strconv.FormatInt(r.ID, 10), Body: r.Body, Author: r.User.Login, Path: r.Path, URL: r.URL, UpdatedAt: r.UpdatedAt})
		}
		if len(rows) < 100 {
			return result, nil
		}
	}
	return nil, fmt.Errorf("forgejo review comments exceed pagination limit")
}
