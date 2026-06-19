package forgejo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// Checks returns the combined CI/build status for a commit via
// Forgejo/Gitea's combined commit-status endpoint
// (/repos/{repo}/commits/{sha}/status). Each entry in the response's
// `statuses` array becomes a forge.Check; the overall state is rolled
// up via forge.RollUp. A commit with no statuses comes back as
// CIStateUnknown with no checks (not an error).
//
// Implements forge.Forge.Checks.
func (c *Client) Checks(ctx context.Context, repo, sha string) (forge.CIStatus, forge.RateLimit, error) {
	if sha == "" {
		return forge.CIStatus{State: forge.CIStateUnknown}, forge.RateLimit{}, nil
	}

	path := fmt.Sprintf("/api/v1/repos/%s/commits/%s/status", repo, sha)
	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return forge.CIStatus{}, rl, err
	}
	if status == http.StatusTooManyRequests {
		return forge.CIStatus{State: forge.CIStateUnknown}, rl, nil
	}
	if status == http.StatusNotFound {
		return forge.CIStatus{State: forge.CIStateUnknown}, rl, nil
	}
	if status != http.StatusOK {
		return forge.CIStatus{}, rl, fmt.Errorf("forgejo %s: status %d", path, status)
	}

	var raw struct {
		Statuses []struct {
			Status    string `json:"status"` // success | pending | failure | error | warning
			Context   string `json:"context"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return forge.CIStatus{}, rl, fmt.Errorf("decoding commit status: %w", err)
	}

	checks := make([]forge.Check, 0, len(raw.Statuses))
	for _, s := range raw.Statuses {
		checks = append(checks, forge.Check{
			Name:  s.Context,
			State: fjStatusState(s.Status),
			URL:   s.TargetURL,
		})
	}
	return forge.CIStatus{State: forge.RollUp(checks), Checks: checks}, rl, nil
}

// fjStatusState maps a Forgejo/Gitea commit-status state to a CIState.
func fjStatusState(state string) forge.CIState {
	switch state {
	case "success":
		return forge.CIStateSuccess
	case "pending", "running":
		return forge.CIStatePending
	case "failure", "error":
		return forge.CIStateFailure
	case "warning":
		// Warnings don't block; treat as success so they don't show red.
		return forge.CIStateSuccess
	default:
		return forge.CIStateUnknown
	}
}
