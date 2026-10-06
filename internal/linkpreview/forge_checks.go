package linkpreview

import (
	"context"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
)

// The same forge clients paginate and normalize checks for the sidebar.
func (f Forge) fetchChecks(ctx context.Context, api *API, repo, sha string) (Preview, error) {
	var status forge.CIStatus
	var limit forge.RateLimit
	var err error
	if f.Gitea {
		status, limit, err = forgejo.NewWithCredentials(f.Host, strings.TrimSuffix(f.APIBase, "/api/v1"), api.token, api.client).Checks(ctx, repo, sha)
	} else {
		status, limit, err = github.NewWithCredentials(f.APIBase, api.token, api.client).Checks(ctx, repo, sha)
	}
	if err != nil {
		return Preview{}, err
	}
	if limit.Limited {
		return Preview{}, &HTTPError{Status: http.StatusTooManyRequests}
	}
	return Preview{Checks: &status}, nil
}
