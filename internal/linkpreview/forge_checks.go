package linkpreview

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
)

type previewRefreshKey struct{}

// WithPreviewRefresh bypasses the requested snapshot, retaining grant/rate controls.
func WithPreviewRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, previewRefreshKey{}, true)
}

// The same forge clients paginate and normalize checks for the sidebar.
func (f Forge) fetchChecks(ctx context.Context, api *API, repo, sha string) (Preview, error) {
	client := *api.client
	var status forge.CIStatus
	var limit forge.RateLimit
	var err error
	if f.Gitea {
		status, limit, err = forgejo.NewWithCredentials(f.Host, strings.TrimSuffix(f.APIBase, "/api/v1"), api.token, &client).Checks(ctx, repo, sha)
	} else {
		status, limit, err = github.NewWithCredentials(f.APIBase, api.token, &client).Checks(ctx, repo, sha)
	}
	if err != nil {
		var upstream *forgehttp.ResponseError
		if errors.As(err, &upstream) && (upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden) {
			he := &HTTPError{Status: upstream.Status}
			if upstream.RateLimit.Limited {
				he.Status = http.StatusTooManyRequests
				if !upstream.RateLimit.ResetAt.IsZero() {
					he.RetryAfter = time.Until(upstream.RateLimit.ResetAt)
				}
			}
			return Preview{}, he
		}
		return Preview{}, err
	}
	if limit.Limited {
		var retryAfter time.Duration
		if !limit.ResetAt.IsZero() {
			retryAfter = time.Until(limit.ResetAt)
		}
		return Preview{}, &HTTPError{Status: http.StatusTooManyRequests, RetryAfter: retryAfter}
	}
	if status.Checks == nil {
		status.Checks = []forge.Check{}
	}
	return Preview{Checks: &status, Stale: limit.Stale}, nil
}
