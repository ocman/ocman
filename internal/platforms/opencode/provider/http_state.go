package opencode

import (
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// httpState is owned by a named account. A nil receiver preserves the package
// helpers and legacy adapter's process-default clients without copying caches.
type httpState struct {
	api, llm *http.Client
	events   *http.Client
	catalog  *httpCache
	stream   http.RoundTripper
}

func legacyHTTP() *httpState { return nil }

func newHTTPState(auth ocapi.Auth) *httpState {
	stream := auth.IsolatedTransport(http.DefaultTransport)
	transport := otelhttp.NewTransport(stream)
	return &httpState{
		api:     &http.Client{Timeout: 10 * time.Second, Transport: transport},
		llm:     &http.Client{Transport: transport},
		events:  &http.Client{Transport: stream},
		catalog: newHTTPCacheNamed(30*time.Second, "opencode.connection_catalog_http"),
		stream:  stream,
	}
}

func (h *httpState) client() *http.Client {
	if h == nil {
		return openCodeClient
	}
	return h.api
}

func (h *httpState) llmClient() *http.Client {
	if h == nil {
		return openCodeLLMClient
	}
	return h.llm
}

func (h *httpState) cache() *httpCache {
	if h == nil {
		return catalogCache
	}
	return h.catalog
}
