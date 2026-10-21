package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// openCodeClient is an HTTP client with a reasonable timeout for API
// calls to local OpenCode instances.
var openCodeClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: otelhttp.NewTransport(ocapi.New("").Transport(http.DefaultTransport)),
}

// openCodeLLMClient serves the calls whose upstream request blocks
// until a model replies (the title agent, slash commands, summarize):
// the caller's context bounds the wait instead of the read timeout. Same
// transport chain (otelhttp → auth → ocv2.Wrap) as openCodeClient.
var openCodeLLMClient = &http.Client{
	Transport: openCodeClient.Transport,
}

func configureHTTPAuth(auth ocapi.Auth) {
	// Keep auth inside instrumentation so even opt-in HTTP header capture
	// observes the original request without Authorization.
	transport := otelhttp.NewTransport(auth.Transport(http.DefaultTransport))
	openCodeClient.Transport = transport
	openCodeLLMClient.Transport = transport
}

// --- Fetching session data from the OpenCode HTTP API ---

// rawGet performs a plain GET against the OpenCode instance and
// returns the response body if the status is 200.
func rawGet(port, path string) ([]byte, bool) {
	return rawGetContext(context.Background(), port, path)
}

// rawGetContext is rawGet bound to ctx, so a cancelled caller stops the
// upstream read instead of letting it run to completion.
func rawGetContext(ctx context.Context, port, path string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s%s", port, path), nil)
	if err != nil {
		return nil, false
	}
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	// Conversation payloads are the biggest thing ocman reads from
	// OpenCode; bound them so a runaway response can't be buffered whole.
	body, err := readLimited(resp.Body, maxUpstreamConversationBytes)
	if err != nil {
		logFetchFailure(err, log.Fields{"port": port, "path": path}, "opencode: upstream response refused")
		return nil, false
	}
	return body, true
}

// getInto performs a plain GET against the OpenCode instance and
// unmarshals a 200 JSON response into v. Returns false on any
// transport, status, or decode failure.
func getInto(port, path string, v interface{}) bool {
	body, ok := rawGet(port, path)
	if !ok {
		return false
	}
	return json.Unmarshal(body, v) == nil
}

// fetchOpenCodeSmallModel fetches the resolved OpenCode config and extracts small_model.
func fetchOpenCodeSmallModel(port string) (providerID, modelID string, ok bool) {
	var cfg struct {
		SmallModel string `json:"small_model"`
	}
	if !getInto(port, "/config", &cfg) {
		return "", "", false
	}
	slash := strings.IndexByte(cfg.SmallModel, '/')
	if slash <= 0 || slash == len(cfg.SmallModel)-1 {
		return "", "", false
	}
	return cfg.SmallModel[:slash], cfg.SmallModel[slash+1:], true
}

// OpenCodeProviderModel is the minimal subset of a model entry needed for the picker.
type OpenCodeProviderModel struct {
	ID       string                          `json:"id"`
	Name     string                          `json:"name,omitempty"`
	Status   string                          `json:"status,omitempty"`
	Variants map[string]OpenCodeModelVariant `json:"variants,omitempty"`
	Limit    OpenCodeModelLimit              `json:"limit,omitempty"`
}

// OpenCodeModelLimit mirrors the `limit` block on a model entry.
type OpenCodeModelLimit struct {
	Context int64 `json:"context,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

// OpenCodeModelVariant is a single variant entry from OpenCode's /provider payload.
type OpenCodeModelVariant struct {
	Disabled bool `json:"disabled,omitempty"`
}

// OpenCodeProvider is a trimmed provider entry.
type OpenCodeProvider struct {
	ID     string                           `json:"id"`
	Name   string                           `json:"name,omitempty"`
	Models map[string]OpenCodeProviderModel `json:"models"`
}

// OpenCodeProvidersResponse is the shape returned by OpenCode's GET /provider.
type OpenCodeProvidersResponse struct {
	All       []OpenCodeProvider `json:"all"`
	Connected []string           `json:"connected"`
	Default   map[string]string  `json:"default"`
}

// fetchOpenCodeProviders calls GET /provider on the running OpenCode
// instance, scoped to directory on an OpenCode v2 server.
func fetchOpenCodeProviders(ctx context.Context, port, directory string) (OpenCodeProvidersResponse, bool) {
	var empty OpenCodeProvidersResponse
	body, err := getJSONCached(ctx, port, scopedPath(ctx, port, "/provider", directory))
	if err != nil {
		logFetchFailure(err, log.Fields{"port": port, "endpoint": "/provider"},
			"opencode: provider catalog fetch failed")
		return empty, false
	}
	var parsed OpenCodeProvidersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return empty, false
	}
	return parsed, true
}

// scopedPath adds the directory scope to a catalog path on an OpenCode v2
// server: one v2 server serves every project, and agents, commands and
// models are per-project configuration. The path is also the cache key,
// so projects never share an entry. v1 instances serve one project and
// keep the bare path.
func scopedPath(ctx context.Context, port, path, directory string) string {
	if directory == "" || !ocv2.IsV2(ctx, port) {
		return path
	}
	return path + "?directory=" + url.QueryEscape(directory)
}
