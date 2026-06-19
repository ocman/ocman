package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// Checks returns the combined CI/build status for a commit by merging
// GitHub's two CI mechanisms:
//
//   - legacy commit statuses (/commits/{sha}/status) — external CI
//     and the older Status API,
//   - check runs (/commits/{sha}/check-runs) — GitHub Actions and
//     modern Checks API apps.
//
// Both are queried; the per-check lists are concatenated and the
// overall state is rolled up via forge.RollUp. A commit with no CI
// configured comes back as CIStateUnknown with no checks (not an
// error).
//
// Implements forge.Forge.Checks.
func (c *Client) Checks(ctx context.Context, repo, sha string) (forge.CIStatus, forge.RateLimit, error) {
	if sha == "" {
		return forge.CIStatus{State: forge.CIStateUnknown}, forge.RateLimit{}, nil
	}

	statuses, rl, err := c.commitStatuses(ctx, repo, sha)
	if err != nil {
		return forge.CIStatus{}, rl, err
	}
	runs, rl2, err := c.checkRuns(ctx, repo, sha)
	if err != nil {
		return forge.CIStatus{}, rl2, err
	}
	// Prefer the most recent rate-limit reading.
	if rl2.Limited {
		rl = rl2
	}

	checks := append(statuses, runs...)
	return forge.CIStatus{State: forge.RollUp(checks), Checks: checks}, rl, nil
}

// commitStatuses fetches the legacy combined commit status. Returns an
// empty slice (not an error) when the commit has no statuses.
func (c *Client) commitStatuses(ctx context.Context, repo, sha string) ([]forge.Check, forge.RateLimit, error) {
	path := fmt.Sprintf("/repos/%s/commits/%s/status", repo, sha)
	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status == http.StatusNotFound {
		// No commit / no statuses — treat as "no checks".
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("github api %s: status %d", path, status)
	}

	var raw struct {
		Statuses []struct {
			State     string `json:"state"` // success | pending | failure | error
			Context   string `json:"context"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding commit status: %w", err)
	}

	out := make([]forge.Check, 0, len(raw.Statuses))
	for _, s := range raw.Statuses {
		out = append(out, forge.Check{
			Name:  s.Context,
			State: ghStatusState(s.State),
			URL:   s.TargetURL,
		})
	}
	return out, rl, nil
}

// checkRuns fetches check runs (GitHub Actions etc.) for the commit.
func (c *Client) checkRuns(ctx context.Context, repo, sha string) ([]forge.Check, forge.RateLimit, error) {
	path := fmt.Sprintf("/repos/%s/commits/%s/check-runs", repo, sha)
	body, rl, status, err := c.fetch(ctx, path)
	if err != nil {
		return nil, rl, err
	}
	if status == http.StatusTooManyRequests {
		return nil, rl, nil
	}
	if status == http.StatusNotFound {
		return nil, rl, nil
	}
	if status != http.StatusOK {
		return nil, rl, fmt.Errorf("github api %s: status %d", path, status)
	}

	var raw struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`     // queued | in_progress | completed
			Conclusion string `json:"conclusion"` // success | failure | neutral | cancelled | timed_out | action_required | stale | skipped
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, rl, fmt.Errorf("decoding check-runs: %w", err)
	}

	out := make([]forge.Check, 0, len(raw.CheckRuns))
	for _, cr := range raw.CheckRuns {
		out = append(out, forge.Check{
			Name:  cr.Name,
			State: ghCheckRunState(cr.Status, cr.Conclusion),
			URL:   cr.HTMLURL,
		})
	}
	return out, rl, nil
}

// ghStatusState maps a legacy commit-status state to a CIState.
func ghStatusState(state string) forge.CIState {
	switch state {
	case "success":
		return forge.CIStateSuccess
	case "pending":
		return forge.CIStatePending
	case "failure", "error":
		return forge.CIStateFailure
	default:
		return forge.CIStateUnknown
	}
}

// ghCheckRunState maps a check run's (status, conclusion) pair to a
// CIState. A run that hasn't completed is pending regardless of
// conclusion; skipped/neutral runs don't count as failures.
func ghCheckRunState(status, conclusion string) forge.CIState {
	if status != "completed" {
		return forge.CIStatePending
	}
	switch conclusion {
	case "success":
		return forge.CIStateSuccess
	case "neutral", "skipped":
		return forge.CIStateSuccess
	case "failure", "timed_out", "cancelled", "action_required", "stale":
		return forge.CIStateFailure
	default:
		return forge.CIStateUnknown
	}
}
