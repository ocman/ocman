package forgehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// ReviewApproval reads both forges' REST review shapes. Approval means at least
// one current approval and no outstanding change requests, not permission to merge.
func ReviewApproval(ctx context.Context, fetch func(context.Context, string) ([]byte, forge.RateLimit, int, error), path, pageSizeParam string) (bool, error) {
	type review struct {
		ID          int64     `json:"id"`
		State       string    `json:"state"`
		Dismissed   bool      `json:"dismissed"`
		SubmittedAt time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	latest := make(map[string]review)
	for page := 1; ; page++ {
		body, rl, status, err := fetch(ctx, fmt.Sprintf("%s?%s=30&page=%d", path, pageSizeParam, page))
		if err != nil {
			return false, err
		}
		if status != http.StatusOK {
			return false, &ResponseError{Status: status, RateLimit: rl}
		}
		var reviews []review
		if err := json.Unmarshal(body, &reviews); err != nil {
			return false, fmt.Errorf("decoding reviews: %w", err)
		}
		for _, r := range reviews {
			r.State = strings.ToUpper(r.State)
			if r.State == "REQUEST_CHANGES" {
				r.State = "CHANGES_REQUESTED"
			}
			if r.User.Login == "" || (r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" && r.State != "DISMISSED") {
				continue
			}
			previous := latest[r.User.Login]
			if r.SubmittedAt.After(previous.SubmittedAt) || (r.SubmittedAt.Equal(previous.SubmittedAt) && r.ID > previous.ID) {
				latest[r.User.Login] = r
			}
		}
		if len(reviews) < 30 {
			break
		}
	}
	approved := false
	for _, r := range latest {
		if r.Dismissed {
			continue
		}
		if r.State == "CHANGES_REQUESTED" {
			return false, nil
		}
		approved = approved || r.State == "APPROVED"
	}
	return approved, nil
}
