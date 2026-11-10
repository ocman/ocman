package opencode

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv1"
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

func (h *httpState) getJSON(ctx context.Context, port, path string) ([]byte, error) {
	return apiv1.GetJSON(ctx, h.client(), port, path)
}

// getJSONCached leaves failures uncached so subsequent requests can retry.
func (h *httpState) getJSONCached(ctx context.Context, port, path string) ([]byte, error) {
	var fetchErr error
	body, ok := h.cache().getOrFetchContext(ctx, port, path, func() ([]byte, bool) {
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
		b, err := h.getJSON(fetchCtx, port, path)
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

func (h *httpState) postJSON(ctx context.Context, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, h.client(), http.MethodPost, port, path, payload)
}

// postJSONLLM leaves the deadline to the caller for model-dependent requests.
func (h *httpState) postJSONLLM(ctx context.Context, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, h.llmClient(), http.MethodPost, port, path, payload)
}

func (h *httpState) postJSONForDirectory(ctx context.Context, port, path, directory string, payload []byte) error {
	return h.postJSON(ctx, port, path+"?directory="+url.QueryEscape(directory), payload)
}

func (h *httpState) patchJSON(ctx context.Context, port, path string, payload []byte) error {
	return h.sendJSON(ctx, http.MethodPatch, port, path, payload)
}

func (h *httpState) postJSONReturning(ctx context.Context, port, path string, payload []byte) ([]byte, error) {
	return apiv1.PostJSONReturning(ctx, h.client(), port, path, payload)
}

// postJSONReturningLLM leaves the deadline to the caller for helper requests.
func (h *httpState) postJSONReturningLLM(ctx context.Context, port, path string, payload []byte) ([]byte, error) {
	return apiv1.PostJSONReturning(ctx, h.llmClient(), port, path, payload)
}

func (h *httpState) sendJSON(ctx context.Context, method, port, path string, payload []byte) error {
	return apiv1.SendJSON(ctx, h.client(), method, port, path, payload)
}
