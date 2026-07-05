package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// HTTP plumbing for talking to a running OpenCode instance: TTL
// caches, JSON GET/POST/PATCH helpers, and upstream error extraction.

// --- Helpers ---

// catalogCache is a process-wide TTL cache for upstream OpenCode
// catalog endpoints (/agent, /command, /provider). 30s is the
// trade-off between staleness when the user edits config and
// per-poll cost: at 30s a cold dashboard mount fires one upstream
// call per endpoint, every subsequent mount within the window is
// instant.
//
// The cache is keyed by (port, path), so multiple running OpenCode
// instances coexist correctly. See httpcache.go for the cache
// machinery itself, including the singleflight that coalesces
// concurrent misses for the same key.
var catalogCache = newHTTPCacheNamed(30*time.Second, "opencode.catalog_http")

// sessionCache is a process-wide TTL cache for session-scoped
// OpenCode endpoints — currently /session/{id} and
// /session/{id}/message. It exists to absorb the multi-handler
// fan-out that happens when the user opens a session detail page:
// /api/session/{id} fetches both endpoints, and /api/session/{id}/info
// fetches /session/{id}/message a second time, in parallel. Without
// caching that's 3 simultaneous round-trips for the same session.
//
// 5s TTL is the trade-off between freshness and the bursty
// "user clicks around" pattern: the dashboard fires several
// per-session requests within a short window when the panel mounts,
// then nothing for a few seconds, then another burst when the user
// clicks somewhere new. Below ~3s the cache expires *between*
// bursts, which is the worst of both worlds (we pay full cost on
// the burst's first call, every time). Above ~5s we start serving
// noticeably stale messages while the agent is mid-stream.
//
// Real-time updates for the *currently-viewed* session still come
// through the SSE event stream, which doesn't go through this
// cache. So the cache only affects refreshes triggered by route
// transitions, focus events, etc. — exactly the cases where 5s of
// staleness is invisible. Failures are not cached (see httpcache.go).
var sessionCache = newHTTPCacheNamed(5*time.Second, "opencode.session_http")

// getJSON performs a GET to the OpenCode instance and returns the body
// bytes and true iff the response was 200 OK with a JSON content type.
func getJSON(ctx context.Context, port, path string) ([]byte, bool) {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
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
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	return body, true
}

// getJSONCached is getJSON wrapped through catalogCache. Callers that
// fetch effectively-immutable catalog data should use this; one-shot
// reads of session-specific data must keep using getJSON.
//
// On a hit, no HTTP call is made. On a miss the underlying getJSON
// runs (singleflighted across concurrent callers), and a successful
// 200/JSON response is cached for catalogCache's TTL. Failures are
// not cached — see httpCache.getOrFetch.
func getJSONCached(ctx context.Context, port, path string) ([]byte, bool) {
	return catalogCache.getOrFetch(port, path, func() ([]byte, bool) {
		return getJSON(ctx, port, path)
	})
}

// postJSON performs a POST with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
func postJSON(ctx context.Context, port, path string, payload []byte) error {
	return sendJSON(ctx, http.MethodPost, port, path, payload)
}

// patchJSON performs a PATCH with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
func patchJSON(ctx context.Context, port, path string, payload []byte) error {
	return sendJSON(ctx, http.MethodPatch, port, path, payload)
}

// sendJSON performs an HTTP request with a JSON body. Returns nil on 2xx,
// an error describing the upstream status otherwise.
//
// On a 4xx response the returned error wraps a *platforms.UpstreamError
// so callers can pass the upstream-supplied human message through to
// the UI (errors.Is(err, platforms.ErrUpstreamRejected) will be true).
// 5xx and transport errors fall through as plain wrapped errors and
// land in the default "platform unreachable" bucket on the way out.
func sendJSON(ctx context.Context, method, port, path string, payload []byte) error {
	apiURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return fmt.Errorf("opencode %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode < 500 {
			ue := &platforms.UpstreamError{
				Status:  resp.StatusCode,
				Message: extractOpenCodeErrorMessage(body),
			}
			return fmt.Errorf("opencode %s: %w", path, ue)
		}
		if len(body) == 0 {
			return fmt.Errorf("opencode %s: upstream HTTP %d", path, resp.StatusCode)
		}
		return fmt.Errorf("opencode %s: upstream HTTP %d: %s", path, resp.StatusCode, string(body))
	}
	return nil
}

// extractOpenCodeErrorMessage best-effort parses an OpenCode error
// response body into a single human-readable string suitable for the
// UI. OpenCode's API errors follow the Hono `NamedError` shape:
//
//	{"data":{"providerID":"...","modelID":"..."},"name":"ProviderModelNotFoundError"}
//
// We prefer `data.message` if present (it's already a complete
// sentence), then fall back to combining `name` with any structured
// `data` fields, and finally to the raw body. Returns "" when the
// body is empty so callers can apply their own fallback.
func extractOpenCodeErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Name string                 `json:"name"`
		Tag  string                 `json:"_tag"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return trimmed
	}
	// Some OpenCode errors (e.g. PermissionNotFoundError) use the
	// effect-style `_tag` discriminator instead of Hono's `name`.
	if parsed.Name == "" {
		parsed.Name = parsed.Tag
	}
	if parsed.Name == "" {
		return trimmed
	}
	if msg, ok := parsed.Data["message"].(string); ok && msg != "" {
		return msg
	}
	// Build "<Name>: k=v, k=v" from the structured data so callers
	// see something useful for errors that don't carry a message
	// (e.g. ProviderModelNotFoundError → "providerID=anthropic, modelID=claude-bogus").
	if len(parsed.Data) == 0 {
		return parsed.Name
	}
	keys := make([]string, 0, len(parsed.Data))
	for k := range parsed.Data {
		keys = append(keys, k)
	}
	// Stable order without importing sort: the data maps in practice
	// have <=3 keys; a tiny bubble keeps the test deterministic and
	// avoids the extra import.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, parsed.Data[k]))
	}
	return fmt.Sprintf("%s: %s", parsed.Name, strings.Join(parts, ", "))
}
