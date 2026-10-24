package opencode

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv1"
)

// The provider owns client selection and catalog caching; apiv1 owns bounded
// HTTP operations and upstream error decoding for both native and translated calls.
var agentCatalogFetchSlots = make(chan struct{}, 8)

const (
	maxUpstreamErrorBytes        = apiv1.MaxErrorBytes
	maxUpstreamConversationBytes = apiv1.MaxConversationBytes
)

// catalogCache coalesces requests for effectively immutable configuration.
// Port and path keys isolate instances and directory-scoped v2 catalogs.
var catalogCache = newHTTPCacheNamed(30*time.Second, "opencode.catalog_http")

func getJSON(ctx context.Context, port, path string) ([]byte, error) {
	return apiv1.GetJSON(ctx, openCodeClient, port, path)
}

// getJSONCached leaves failures uncached so subsequent requests can retry.
func getJSONCached(ctx context.Context, port, path string) ([]byte, error) {
	var fetchErr error
	body, ok := catalogCache.getOrFetchContext(ctx, port, path, func() ([]byte, bool) {
		fetchCtx := ctx
		endpoint, _, _ := strings.Cut(path, "?")
		if endpoint == "/agent" {
			queueCtx, cancelQueue := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancelQueue()
			select {
			case agentCatalogFetchSlots <- struct{}{}:
				defer func() { <-agentCatalogFetchSlots }()
			case <-queueCtx.Done():
				fetchErr = queueCtx.Err()
				return nil, false
			}
			var cancelFetch context.CancelFunc
			fetchCtx, cancelFetch = context.WithTimeout(queueCtx, 2*time.Second)
			defer cancelFetch()
		}
		b, err := getJSON(fetchCtx, port, path)
		fetchErr = err
		return b, err == nil
	})
	if !ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fetchErr != nil {
			return nil, fetchErr
		}
		return nil, errFetchFailed
	}
	return body, nil
}

func postJSON(ctx context.Context, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, openCodeClient, http.MethodPost, port, path, payload)
}

// postJSONLLM leaves the deadline to the caller for model-dependent requests.
func postJSONLLM(ctx context.Context, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, openCodeLLMClient, http.MethodPost, port, path, payload)
}

func postJSONForDirectory(ctx context.Context, port, path, directory string, payload []byte) error {
	return postJSON(ctx, port, path+"?directory="+url.QueryEscape(directory), payload)
}

func patchJSON(ctx context.Context, port, path string, payload []byte) error {
	return sendJSON(ctx, http.MethodPatch, port, path, payload)
}

func postJSONReturning(ctx context.Context, port, path string, payload []byte) ([]byte, error) {
	return apiv1.PostJSONReturning(ctx, openCodeClient, port, path, payload)
}

// postJSONReturningLLM leaves the deadline to the caller for helper requests.
func postJSONReturningLLM(ctx context.Context, port, path string, payload []byte) ([]byte, error) {
	return apiv1.PostJSONReturning(ctx, openCodeLLMClient, port, path, payload)
}

func sendJSON(ctx context.Context, method, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, openCodeClient, method, port, path, payload)
}
