package linkpreview

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
)

type checksRefreshKey struct{}

// WithChecksRefresh bypasses only the checks snapshot, retaining grant/rate controls.
func WithChecksRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, checksRefreshKey{}, true)
}

// Checks clients normally format 401/403 as strings. Preserve their status at
// the HTTP boundary so the preview service can revoke or classify the grant.
type checksAuthTransport struct{ http.RoundTripper }

func (t checksAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	return resp, nil
}

// The same forge clients paginate and normalize checks for the sidebar.
func (f Forge) fetchChecks(ctx context.Context, api *API, repo, sha string) (Preview, error) {
	client := *api.client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = checksAuthTransport{transport}
	var status forge.CIStatus
	var limit forge.RateLimit
	var err error
	if f.Gitea {
		status, limit, err = forgejo.NewWithCredentials(f.Host, strings.TrimSuffix(f.APIBase, "/api/v1"), api.token, &client).Checks(ctx, repo, sha)
	} else {
		status, limit, err = github.NewWithCredentials(f.APIBase, api.token, &client).Checks(ctx, repo, sha)
	}
	if err != nil {
		return Preview{}, err
	}
	if limit.Limited {
		var retryAfter time.Duration
		if !limit.ResetAt.IsZero() {
			retryAfter = time.Until(limit.ResetAt)
		}
		return Preview{}, &HTTPError{Status: http.StatusTooManyRequests, RetryAfter: retryAfter}
	}
	return Preview{Checks: &status}, nil
}
