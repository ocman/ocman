package forgejo

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
)

// listQuery builds a list page query; key/value add the endpoint's scoping.
func listQuery(opts forge.ListOptions, key, value string) string {
	opts = opts.WithDefaults()
	q := url.Values{}
	q.Set("state", opts.State)
	q.Set(key, value)
	q.Set("limit", strconv.Itoa(opts.PerPage))
	q.Set("page", strconv.Itoa(opts.Page))
	return q.Encode()
}

func statusErr(path string) forgehttp.StatusError {
	return func(status int, _ forge.RateLimit) error {
		return fmt.Errorf("forgejo %s: status %d", path, status)
	}
}

func toPR(r forgehttp.PR, host, repo string) forge.PR {
	pr := r.ToForge(host, repo, r.Merged)
	// Forgejo retains the original branch in label after deleting the head branch.
	if pr.Branch == fmt.Sprintf("refs/pull/%d/head", r.Number) && r.Head.Label != "" {
		pr.Branch = r.Head.Label
	}
	return pr
}
