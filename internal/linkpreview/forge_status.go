package linkpreview

import (
	"context"
	"strconv"
	"strings"

	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
)

// Use the sidebar's review parser and credential-scoped GitHub HTTP cache.
func (f Forge) fetchApproval(ctx context.Context, api *API, repo, id string) (bool, error) {
	number, err := strconv.Atoi(id)
	if err != nil || !forgeNumber.MatchString(id) {
		return false, ErrUnsafeRequest
	}
	client := *api.client
	var approved bool
	if f.Gitea {
		approved, err = forgejo.NewWithCredentials(f.Host, strings.TrimSuffix(f.APIBase, "/api/v1"), api.token, &client).PRApproval(ctx, repo, number)
	} else {
		approved, err = github.NewWithCredentials(f.APIBase, api.token, &client).PRApproval(ctx, repo, number)
	}
	return approved, forgePreviewError(err)
}
