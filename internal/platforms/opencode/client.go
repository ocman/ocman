package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// rePortSuffix matches a port number at the end of a string (e.g. ":4096").
var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

// openCodeClient is an HTTP client with a reasonable timeout for API
// calls to local OpenCode instances. The transport is wrapped with
// otelhttp so every request becomes a child span of the in-flight
// server span — when telemetry is disabled the wrapping is a no-op
// (the global TracerProvider is the SDK noop).
var openCodeClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: otelhttp.NewTransport(http.DefaultTransport),
}

// limitedReader wraps a byte slice in a reader for HTTP request bodies.
func limitedReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}

// --- Port discovery with TTL cache ---

// portCache holds cached port discovery results. Reads are guarded
// by an RWMutex so cache hits are non-blocking even under heavy
// concurrency; writes happen briefly after the singleflight-protected
// lsof scan completes.
var portCache struct {
	mu      sync.RWMutex
	ports   map[string]string
	updated time.Time
}

// portFlight coalesces concurrent cold-cache callers into a single
// underlying lsof invocation. Without it, a SessionDetail mount
// firing five endpoints simultaneously paid the full lsof cost for
// each because the previous mutex-around-lsof pattern serialized
// the callers but didn't dedupe the work past the brief cache-hit
// window after the first one finished.
var portFlight singleflight.Group

// portCacheTTL bounds how long a discovered port map is reused
// before the next request triggers a fresh lsof scan. The trade-off
// is staleness when the user starts a new OpenCode instance vs.
// per-request lsof cost: lsof is bounded but not free (single-digit
// ms for the global scan, plus 30+ ms per running OpenCode for the
// cwd lookup), and the only thing a stale cache can produce is
// momentarily "no live connection" for a freshly-started instance —
// which the next refresh corrects within one TTL.
//
// 10s is the sweet spot in practice. The dashboard polls every
// ~5s, so warm hits dominate; cold misses become a once-per-poll
// cost rather than once-per-other-poll. If the user starts a new
// OpenCode instance, the live-connection badge will lag by at most
// one cycle, which is below the typical perception threshold.
const portCacheTTL = 10 * time.Second

// discoverPortsImpl is the indirection used so tests can swap out the
// expensive lsof execution. Production assigns it once, lazily, to
// the real implementation; tests override it before calling
// discoverOpenCodePorts.
var discoverPortsImpl = discoverOpenCodePortsUncached

// resetPortCacheForTests clears the cache so each test starts with a
// cold path. Not exported — only callable from within the package.
func resetPortCacheForTests() {
	portCache.mu.Lock()
	portCache.ports = nil
	portCache.updated = time.Time{}
	portCache.mu.Unlock()
	// singleflight.Group has no public reset; that's fine since
	// in-flight calls will complete on their own and any post-test
	// caller starts fresh.
}

// discoverOpenCodePorts returns a map of directory -> port for all running
// OpenCode instances that are listening on TCP ports.
// Results are cached for a few seconds to avoid calling lsof on every request.
//
// Cold-path behaviour: at most one in-flight lsof scan, even when
// many callers race past the cache check. Singleflight makes
// concurrent callers share that one scan; the read lock makes warm
// hits non-blocking.
func discoverOpenCodePorts() map[string]string {
	if cached, ok := readCachedPorts(); ok {
		return cached
	}

	const flightKey = "discoverOpenCodePorts"
	v, _, _ := portFlight.Do(flightKey, func() (interface{}, error) {
		// Re-check inside the singleflight body in case another
		// caller filled the cache between our miss and acquiring
		// the flight slot.
		if cached, ok := readCachedPorts(); ok {
			return cached, nil
		}
		result := discoverPortsImpl()
		portCache.mu.Lock()
		portCache.ports = result
		portCache.updated = time.Now()
		portCache.mu.Unlock()
		return copyMap(result), nil
	})
	if m, ok := v.(map[string]string); ok {
		return m
	}
	// Defensive: this branch is not reachable because the
	// singleflight body never returns a non-map value, but keeping
	// the fallback means a future refactor that introduces an error
	// path can't crash the caller.
	return map[string]string{}
}

// readCachedPorts returns the cached port map iff the entry is fresh.
// Lock-light: an RWMutex read lock allows N concurrent cache hits.
func readCachedPorts() (map[string]string, bool) {
	portCache.mu.RLock()
	defer portCache.mu.RUnlock()
	if time.Since(portCache.updated) < portCacheTTL && portCache.ports != nil {
		return copyMap(portCache.ports), true
	}
	return nil, false
}

// copyMap returns a shallow copy of a string map to prevent callers from
// mutating the cached data.
func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// discoverOpenCodePortsUncached performs the actual lsof-based discovery.
//
// Two-phase: first a single global `lsof -iTCP -sTCP:LISTEN` enumerates
// every opencode process listening on TCP and parses out (pid, port)
// pairs, then a bounded-concurrency fan-out runs `lsof -a -p <pid> -d
// cwd` against each candidate to recover its working directory.
//
// Why parallelize the second phase: each per-pid lsof is fast in
// isolation (~30 ms) but one is fired per running OpenCode instance,
// and users can easily accumulate 20+ stale instances across terminal
// tabs. At that scale a sequential walk dominates discovery cost
// (~700 ms in production observation, sequenced behind the 3s port
// cache so every miss bills the user). Per-pid lsof calls don't
// compete for any shared resource — they're pure subprocess +
// per-process file-table lookup — so concurrency scales nearly
// linearly. We cap at 16 workers to stay well under the default
// macOS file-descriptor ulimit (256) even when called from a busy
// server with other open fds.
func discoverOpenCodePortsUncached() map[string]string {
	result := make(map[string]string)

	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return result
	}

	// Parse tabular output to find opencode PIDs and their listen ports.
	// Example line: opencode  91024 dries   15u  IPv4 ... TCP 127.0.0.1:4096 (LISTEN)
	type pidPort struct {
		pid  string
		port string
	}
	var candidates []pidPort
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		if fields[0] != "opencode" {
			continue
		}
		pid := fields[1]
		// Validate PID is numeric to prevent injection
		if _, err := strconv.Atoi(pid); err != nil {
			log.WithField("pid", pid).Warn("skipping non-numeric PID in lsof output")
			continue
		}
		name := fields[len(fields)-2]
		m := rePortSuffix.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		candidates = append(candidates, pidPort{pid: pid, port: m[1]})
	}

	if len(candidates) == 0 {
		return result
	}

	// Resolve each candidate's cwd in parallel. Tuned for the
	// "many stale instances" case (20+ on a developer machine);
	// for the typical 1–2 instances the worker count is bounded by
	// len(candidates) so there's no overhead.
	const maxWorkers = 16
	workers := maxWorkers
	if len(candidates) < workers {
		workers = len(candidates)
	}

	type cwdResult struct {
		dir  string
		port string
	}
	jobs := make(chan pidPort, len(candidates))
	results := make(chan cwdResult, len(candidates))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				cwdOut, err := exec.Command("lsof", "-a", "-p", c.pid, "-d", "cwd", "-F", "n").Output()
				if err != nil {
					continue
				}
				for _, line := range strings.Split(string(cwdOut), "\n") {
					if strings.HasPrefix(line, "n/") {
						results <- cwdResult{dir: line[1:], port: c.port}
						break
					}
				}
			}
		}()
	}

	for _, c := range candidates {
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	close(results)

	for r := range results {
		result[r.dir] = r.port
	}

	return result
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory, or "" if no
// such instance is currently known.
//
// A miss does NOT invalidate the cache. The cache TTL (portCacheTTL)
// is short enough that a genuinely-new OpenCode instance is picked up
// within one cycle. The previous "invalidate on miss" behaviour
// caused 2× lsof per call for any session whose OpenCode instance
// had stopped, AND poisoned the cache for every other concurrent
// reader (dashboard polling, /api/session/:id/info, /models, ...) —
// turning a single dead session view into a system-wide cache
// thrash.
func discoverOpenCodePort(directory string) string {
	return discoverOpenCodePorts()[directory]
}

// discoverOpenCodePortCtx is discoverOpenCodePort with Server-Timing
// instrumentation. Records "lsof_hit" when the port-cache returned a
// fresh value (sub-millisecond) and "lsof_miss" when the lsof scan
// actually ran. Use the ctx-aware variant from any code path that
// has a request context; the bare discoverOpenCodePort remains for
// internal helpers that don't.
func discoverOpenCodePortCtx(ctx context.Context, directory string) string {
	if cached, ok := readCachedPorts(); ok {
		// Cache hit: still record it so the trace shows the port
		// resolution happened at all, but End() right away.
		hit := srvtiming.Begin(ctx, "lsof_hit")
		port := cached[directory]
		hit.End()
		return port
	}
	miss := srvtiming.Begin(ctx, "lsof_miss")
	port := discoverOpenCodePort(directory)
	miss.EndWithDesc("ran fresh lsof scan")
	return port
}

// pendingPromptTimeout caps how long the dashboard's
// `/api/sessions` fan-out will wait on any single OpenCode instance
// for its `/permission` or `/question` list.
//
// Why so tight? Pending-prompt status is a UI hint — it lights up a
// badge on the session row. The shared openCodeClient timeout
// (10 s) is appropriate for the rest of the proxy traffic, but a
// single hung OpenCode instance with that timeout dragged every
// dashboard poll to 10 s+ and queued requests piled up behind it.
//
// 500 ms is well above the p99 of a healthy local response (single-
// digit ms over loopback) but short enough that the dashboard
// recovers within one poll cycle when an instance becomes
// unresponsive.
const pendingPromptTimeout = 500 * time.Millisecond

// pendingPromptCacheTTL bounds the staleness of the cached
// /permission and /question responses. 3 seconds is the same TTL as
// the lsof port cache and is short enough that prompt indicators on
// other-than-current sessions feel live, while eliminating
// roughly 80% of the upstream calls these endpoints would otherwise
// receive (dashboard 5s × notify 10s × favicon + bell × N
// instances).
//
// Real-time prompt updates for the *currently-viewed* session still
// arrive via the SSE stream — this cache only affects the
// dashboard's per-row badge and the favicon/bell pollers, all of
// which are already polling-based.
//
// var rather than const so tests can dial it down without sleeping
// for the full 3 seconds.
var pendingPromptCacheTTL = 3 * time.Second

// pendingPromptCache caches raw /permission and /question response
// bytes per (port, path). Writers swap the cache instance via
// swapPendingPromptCacheTTL when a test wants a non-default TTL;
// during normal operation it's a single long-lived instance.
var pendingPromptCache = newHTTPCache(pendingPromptCacheTTL)

// swapPendingPromptCacheTTL replaces the package-level cache with a
// fresh instance using the supplied TTL. Test-only.
func swapPendingPromptCacheTTL(ttl time.Duration) {
	pendingPromptCacheTTL = ttl
	pendingPromptCache = newHTTPCache(ttl)
}

// resetPendingPromptCache empties the cache so each test starts
// from a cold state.
func resetPendingPromptCache() {
	pendingPromptCache = newHTTPCache(pendingPromptCacheTTL)
}

// fetchPendingPrompts calls the OpenCode HTTP endpoints that list currently
// open permission and question prompts and returns a set of session IDs that
// have an outstanding prompt of each kind. Endpoints that return non-JSON or
// HTTP errors are treated as empty (the endpoint may not be implemented on
// older OpenCode versions — we never want session listing to fail because of
// this best-effort lookup).
func fetchPendingPrompts(port string) (permissions, questions map[string]bool) {
	permissions = fetchPromptSessionIDs(port, "/permission")
	questions = fetchPromptSessionIDs(port, "/question")
	return permissions, questions
}

// fetchPromptSessionIDs returns the set of session IDs mentioned in
// the OpenCode /permission or /question JSON array response.
//
// Routed through pendingPromptCache (TTL pendingPromptCacheTTL) so
// the dashboard's polling fan-out doesn't re-fetch identical data
// every 5 seconds × N running instances. The HTTP call itself uses
// a per-call context capped at pendingPromptTimeout so a hung
// upstream instance can't block the dashboard for the shared
// openCodeClient timeout.
//
// Failures are not cached — if the upstream is unreachable, the
// next poll retries rather than waiting for the cache TTL.
func fetchPromptSessionIDs(port, path string) map[string]bool {
	body, ok := pendingPromptCache.getOrFetch(port, path, func() ([]byte, bool) {
		return getPromptBytes(port, path)
	})
	if !ok {
		return map[string]bool{}
	}
	return parsePromptSessionIDs(body)
}

// getPromptBytes performs the timeout-bounded HTTP fetch for a
// /permission or /question endpoint and returns the raw response
// bytes on success. A non-200, non-JSON, or transport error returns
// (nil, false) — the cache treats this as a no-op and the caller
// returns an empty result.
func getPromptBytes(port, path string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), pendingPromptTimeout)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

// parsePromptSessionIDs extracts the set of session IDs from a
// /permission or /question JSON array response. Tolerates malformed
// entries — anything without a string `sessionID` is silently
// dropped, mirroring the original handler's permissive behaviour.
func parsePromptSessionIDs(body []byte) map[string]bool {
	result := map[string]bool{}
	var items []map[string]interface{}
	if err := json.Unmarshal(body, &items); err != nil {
		return result
	}
	for _, item := range items {
		if sid, ok := item["sessionID"].(string); ok && sid != "" {
			result[sid] = true
		}
	}
	return result
}

// collectPendingPromptsByDir queries every running OpenCode instance for its
// currently pending permission and question prompts and returns two maps,
// each keyed by session ID. Directories that fail to respond are silently
// skipped — this is a best-effort UI hint.
func collectPendingPromptsByDir(ports map[string]string) (permSIDs, questionSIDs map[string]bool) {
	permSIDs = map[string]bool{}
	questionSIDs = map[string]bool{}
	if len(ports) == 0 {
		return permSIDs, questionSIDs
	}

	type result struct {
		perms     map[string]bool
		questions map[string]bool
	}
	results := make(chan result, len(ports))
	var wg sync.WaitGroup
	for _, port := range ports {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			perms, questions := fetchPendingPrompts(p)
			results <- result{perms: perms, questions: questions}
		}(port)
	}
	wg.Wait()
	close(results)

	for r := range results {
		for sid := range r.perms {
			permSIDs[sid] = true
		}
		for sid := range r.questions {
			questionSIDs[sid] = true
		}
	}
	return permSIDs, questionSIDs
}

// --- Fetching session data from the OpenCode HTTP API ---

const maxOutputLen = 200000

// truncatePartOutput limits the size of tool call outputs and large text
// in a part to prevent massive responses.
func truncatePartOutput(part map[string]interface{}) {
	// Truncate large text content (e.g. file reads)
	if text, ok := part["text"].(string); ok && len(text) > maxOutputLen {
		part["text"] = text[:maxOutputLen] + "\n... (truncated)"
	}

	state, ok := part["state"].(map[string]interface{})
	if !ok {
		return
	}
	// Truncate state.output
	if output, ok := state["output"].(string); ok && len(output) > maxOutputLen {
		state["output"] = output[:maxOutputLen] + "\n... (truncated)"
	}
	// Truncate state.metadata.output
	if meta, ok := state["metadata"].(map[string]interface{}); ok {
		if output, ok := meta["output"].(string); ok && len(output) > maxOutputLen {
			meta["output"] = output[:maxOutputLen] + "\n... (truncated)"
		}
	}
}

// fetchOpenCodeSession fetches session metadata from the OpenCode HTTP API.
//
// Routed through sessionCache (1.5s TTL + singleflight) so concurrent
// requests for the same (port, sessionID) coalesce into one upstream
// call. The typical trigger is a SessionDetail mount that fires both
// /api/session/{id} and /api/session/{id}/info in parallel — both
// land here, and the second one gets the cached response.
func fetchOpenCodeSession(port, sessionID string) (map[string]interface{}, error) {
	path := "/session/" + sessionID
	body, ok := sessionCache.getOrFetch(port, path, func() ([]byte, bool) {
		return rawGet(port, path)
	})
	if !ok {
		return nil, fmt.Errorf("session API: upstream fetch failed")
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	return result, nil
}

// rawGet performs a plain GET against the OpenCode instance and
// returns the response body if the status is 200. Used by the
// short-TTL sessionCache wrappers; mirrors getJSON but without the
// content-type assertion (session/message responses don't always set
// the header consistently across OpenCode versions, and the parsing
// step downstream provides the same guard).
func rawGet(port, path string) ([]byte, bool) {
	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	return body, true
}

// fetchOpenCodeSmallModel fetches the resolved OpenCode config from the running
// instance and extracts the `small_model` field, returning providerID/modelID.
// Returns ok=false when the config is unreachable, missing the field, or
// malformed. OpenCode's /config endpoint returns the merged config across
// global/project/custom sources, so this honors whatever precedence the user
// configured. The expected format is `"provider/model"` (e.g.
// `"anthropic/claude-haiku-4-5"`).
func fetchOpenCodeSmallModel(port string) (providerID, modelID string, ok bool) {
	url := fmt.Sprintf("http://127.0.0.1:%s/config", port)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}
	var cfg struct {
		SmallModel string `json:"small_model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", "", false
	}
	slash := strings.IndexByte(cfg.SmallModel, '/')
	if slash <= 0 || slash == len(cfg.SmallModel)-1 {
		return "", "", false
	}
	return cfg.SmallModel[:slash], cfg.SmallModel[slash+1:], true
}

// OpenCodeProviderModel is the minimal subset of a model entry we need for
// the picker. The /provider payload includes costs, capabilities, limits,
// etc. — we strip most of it server-side to keep the frontend response
// small, but preserve variant names so the UI can offer a reasoning picker.
//
// `Limit` carries the model's context-window size, consumed by the
// SessionInfo panel to compute "% used". It's preserved on the Go
// type but excluded from the picker's wire payload (the buildSessionModelEntries
// path strips it explicitly when constructing SessionModelEntry).
type OpenCodeProviderModel struct {
	ID       string                          `json:"id"`
	Name     string                          `json:"name,omitempty"`
	Status   string                          `json:"status,omitempty"`
	Variants map[string]OpenCodeModelVariant `json:"variants,omitempty"`
	Limit    OpenCodeModelLimit              `json:"limit,omitempty"`
}

// OpenCodeModelLimit mirrors the `limit` block on a model entry:
// `context` is the input-window size in tokens; `output` is the
// max-completion size. We only consume `context` today.
type OpenCodeModelLimit struct {
	Context int64 `json:"context,omitempty"`
	Output  int64 `json:"output,omitempty"`
}

// OpenCodeModelVariant is a single variant entry from OpenCode's /provider
// payload. We only need the key (map key) and whether it's disabled; the
// actual option values (reasoningEffort, budgetTokens, …) are opaque to
// ocman — OpenCode applies them when it receives the variant name.
type OpenCodeModelVariant struct {
	Disabled bool `json:"disabled,omitempty"`
}

// OpenCodeProvider is a trimmed provider entry. `Models` matches OpenCode's
// native shape: a map keyed by model ID.
type OpenCodeProvider struct {
	ID     string                           `json:"id"`
	Name   string                           `json:"name,omitempty"`
	Models map[string]OpenCodeProviderModel `json:"models"`
}

// OpenCodeProvidersResponse is the shape returned by OpenCode's GET /provider:
// the full catalog (`all`), the user's authenticated providers (`connected`),
// and the per-provider default model (`default`). `/provider` is preferred
// over `/config/providers` because it also exposes the `connected` set.
type OpenCodeProvidersResponse struct {
	All       []OpenCodeProvider `json:"all"`
	Connected []string           `json:"connected"`
	Default   map[string]string  `json:"default"`
}

// fetchOpenCodeProviders calls GET /provider on the running OpenCode instance
// and returns the catalog of providers, the subset the user has authenticated,
// and the per-provider defaults. Returns ok=false when the endpoint is
// unreachable or responds with a non-200 status so callers can fall back
// gracefully (e.g. to DB-derived recent models).
//
// Routed through catalogCache (see operations.go) — the /provider
// payload is the largest of the three catalog responses (≈3s
// uncached on a cold mount per `__ocmanPerf`) and changes only when
// the user reconfigures providers, so it's the most valuable
// candidate for the 30s TTL cache.
func fetchOpenCodeProviders(port string) (OpenCodeProvidersResponse, bool) {
	var empty OpenCodeProvidersResponse
	body, ok := getJSONCached(context.Background(), port, "/provider")
	if !ok {
		return empty, false
	}
	var parsed OpenCodeProvidersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return empty, false
	}
	return parsed, true
}

// fetchOpenCodeMessages fetches messages for a session from the OpenCode HTTP API.
//
// Routed through sessionCache (1.5s TTL + singleflight) — see
// fetchOpenCodeSession's docstring. The /session/{id}/message
// payload is the largest session-scoped response (full message
// history with tool outputs) and is fetched by both
// /api/session/{id} and /api/session/{id}/info on a typical detail
// mount; the cache collapses those into a single round-trip.
func fetchOpenCodeMessages(port, sessionID string) ([]map[string]interface{}, error) {
	path := "/session/" + sessionID + "/message"
	body, ok := sessionCache.getOrFetch(port, path, func() ([]byte, bool) {
		return rawGet(port, path)
	})
	if !ok {
		return nil, fmt.Errorf("messages API: upstream fetch failed")
	}
	var result []map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding messages: %w", err)
	}
	return result, nil
}

// convertOpenCodeMessages transforms raw OpenCode API messages into the format
// expected by the frontend (separate messages and parts arrays).
func convertOpenCodeMessages(ocMessages []map[string]interface{}) (
	messages []map[string]interface{},
	parts []map[string]interface{},
) {
	messages = make([]map[string]interface{}, 0, len(ocMessages))
	parts = make([]map[string]interface{}, 0)

	for _, m := range ocMessages {
		info, _ := m["info"].(map[string]interface{})
		if info == nil {
			continue
		}

		timeData, _ := info["time"].(map[string]interface{})
		timeCreated := int64(0)
		if tc, ok := timeData["created"].(float64); ok {
			timeCreated = int64(tc)
		}

		msgID, _ := info["id"].(string)
		msgSessionID, _ := info["sessionID"].(string)

		// Remove heavy fields we don't need in the frontend
		delete(info, "summary")
		delete(info, "path")

		msg := map[string]interface{}{
			"id":          msgID,
			"sessionId":   msgSessionID,
			"timeCreated": timeCreated,
			"data":        info,
		}
		messages = append(messages, msg)

		// Extract parts
		if msgParts, ok := m["parts"].([]interface{}); ok {
			for _, p := range msgParts {
				part, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				// Skip non-essential part types
				partType, _ := part["type"].(string)
				if partType == "step-start" || partType == "step-finish" || partType == "snapshot" {
					continue
				}
				// Truncate large outputs to keep response size manageable
				truncatePartOutput(part)
				partEntry := map[string]interface{}{
					"id":        part["id"],
					"messageId": part["messageID"],
					"sessionId": part["sessionID"],
					"data":      part,
				}
				parts = append(parts, partEntry)
			}
		}
	}
	return messages, parts
}

// computeMessageStats aggregates token counts, cost, and duration from converted messages.
type messageStats struct {
	totalInputTokens  float64
	totalOutputTokens float64
	totalCost         float64
	durationMs        int64
	contextTokenCount float64 // context usage for composer display
}

func computeMessageStats(messages []map[string]interface{}) messageStats {
	var stats messageStats
	var firstTime, lastTime float64

	for _, m := range messages {
		info, _ := m["data"].(map[string]interface{})
		if info == nil {
			continue
		}
		if t, ok := m["timeCreated"].(int64); ok {
			ft := float64(t)
			if firstTime == 0 || ft < firstTime {
				firstTime = ft
			}
			if ft > lastTime {
				lastTime = ft
			}
		}
		if tokens, ok := info["tokens"].(map[string]interface{}); ok {
			inputTokens := float64(0)
			outputTokens := float64(0)
			reasoningTokens := float64(0)
			cacheReadTokens := float64(0)
			cacheWriteTokens := float64(0)
			if v, ok := tokens["input"].(float64); ok {
				stats.totalInputTokens += v
				inputTokens = v
			}
			if v, ok := tokens["output"].(float64); ok {
				stats.totalOutputTokens += v
				outputTokens = v
			}
			if v, ok := tokens["reasoning"].(float64); ok {
				reasoningTokens = v
			}
			if cache, ok := tokens["cache"].(map[string]interface{}); ok {
				if v, ok := cache["read"].(float64); ok {
					cacheReadTokens = v
				}
				if v, ok := cache["write"].(float64); ok {
					cacheWriteTokens = v
				}
			}
			if role, _ := info["role"].(string); role == "assistant" && outputTokens > 0 {
				stats.contextTokenCount = inputTokens + outputTokens + reasoningTokens + cacheReadTokens + cacheWriteTokens
			}
		}
		if c, ok := info["cost"].(float64); ok {
			stats.totalCost += c
		}
	}
	if lastTime > firstTime {
		stats.durationMs = int64(lastTime - firstTime)
	}
	return stats
}

// paginateUntyped applies pagination to a slice of untyped maps (messages from OpenCode API).
// Returns the paginated slice and a set of message IDs in the page.
func paginateUntyped(messages []map[string]interface{}, limit, offset int) ([]map[string]interface{}, map[string]bool) {
	total := len(messages)
	start := total - offset - limit
	end := total - offset
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	if start >= end {
		return nil, nil
	}

	paged := messages[start:end]
	ids := make(map[string]bool, len(paged))
	for _, m := range paged {
		if id, ok := m["id"].(string); ok {
			ids[id] = true
		}
	}
	return paged, ids
}

// filterPartsUntyped returns only parts whose messageId is in the given set.
func filterPartsUntyped(parts []map[string]interface{}, msgIDs map[string]bool) []map[string]interface{} {
	if msgIDs == nil {
		return nil
	}
	result := make([]map[string]interface{}, 0)
	for _, p := range parts {
		if mid, ok := p["messageId"].(string); ok && msgIDs[mid] {
			result = append(result, p)
		}
	}
	return result
}

// fetchSessionFromOpenCode tries to get session data from the running
// OpenCode HTTP API and returns it as a typed SessionDetail. Returns
// nil, false when the data is not available (no running instance for
// this session's directory, upstream error, etc.) so callers can fall
// back to the DB.
//
// Backwards-compatible shim around fetchSessionFromOpenCodeCtx —
// passes a background context, so timing instrumentation is silently
// dropped. Callers from request handlers should use the Ctx variant.
func (a *Adapter) fetchSessionFromOpenCode(sessionID string, limit, offset int) (*platforms.SessionDetail, bool) {
	return a.fetchSessionFromOpenCodeCtx(context.Background(), sessionID, limit, offset)
}

// fetchSessionFromOpenCodeCtx is fetchSessionFromOpenCode with
// per-phase Server-Timing instrumentation: separate entries for the
// initial DB lookup, the lsof port discovery, and the parallel
// /session/{id} + /session/{id}/message HTTP round-trips.
func (a *Adapter) fetchSessionFromOpenCodeCtx(ctx context.Context, sessionID string, limit, offset int) (*platforms.SessionDetail, bool) {
	if a.db == nil {
		return nil, false
	}
	// First get session from DB to find the directory.
	dbPhase := srvtiming.Begin(ctx, "db_get_session")
	dbSession, err := a.db.GetSession(sessionID)
	dbPhase.End()
	if err != nil {
		return nil, false
	}

	port := discoverOpenCodePortCtx(ctx, dbSession.Directory)
	if port == "" {
		return nil, false
	}

	// Fetch session detail and messages in parallel.
	var ocSession map[string]interface{}
	var ocMessages []map[string]interface{}
	var sessionErr, messagesErr error
	parallelPhase := srvtiming.Begin(ctx, "http_parallel")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_session")
		ocSession, sessionErr = fetchOpenCodeSession(port, sessionID)
		p.EndWithDesc("GET /session/{id}")
	}()
	go func() {
		defer wg.Done()
		p := srvtiming.Begin(ctx, "http_messages")
		ocMessages, messagesErr = fetchOpenCodeMessages(port, sessionID)
		p.EndWithDesc("GET /session/{id}/message")
	}()
	wg.Wait()
	parallelPhase.EndWithDesc("wall-clock for both fetches")

	if sessionErr != nil || messagesErr != nil || ocSession == nil {
		return nil, false
	}

	// Untyped conversion (preserves every OpenCode-specific data key
	// under the message/part .data map). We then re-encode .data into
	// json.RawMessage for the typed Message/Part shape.
	convPhase := srvtiming.Begin(ctx, "convert")
	untypedMessages, untypedParts := convertOpenCodeMessages(ocMessages)
	stats := computeMessageStats(untypedMessages)
	totalMessages := len(untypedMessages)
	pagedMessages, pagedMsgIDs := paginateUntyped(untypedMessages, limit, offset)
	pagedParts := filterPartsUntyped(untypedParts, pagedMsgIDs)
	convPhase.EndWithDesc("convertOpenCodeMessages + paginate")

	defaultsPhase := srvtiming.Begin(ctx, "db_session_defaults")
	defaults, err := getSessionDefaultsCached(a.db, sessionID, dbSession.Directory)
	defaultsPhase.End()
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).
			Warn("opencode: fetching session defaults for live path")
	}

	// Determine status from the last message using shared logic.
	sessionStatus := "done"
	if n := len(untypedMessages); n > 0 {
		if info, ok := untypedMessages[n-1]["data"].(map[string]interface{}); ok {
			role, _ := info["role"].(string)
			finish, _ := info["finish"].(string)
			lastErr := ""
			if _, hasError := info["error"]; hasError {
				lastErr = "true"
			}
			sessionStatus = db.InferSessionStatus(role, finish, lastErr)
		}
	}

	// Count user messages for messageCount parity with the DB path.
	userMsgCount := 0
	for _, m := range untypedMessages {
		if info, ok := m["data"].(map[string]interface{}); ok {
			if role, _ := info["role"].(string); role == "user" {
				userMsgCount++
			}
		}
	}

	typedPhase := srvtiming.Begin(ctx, "typed")
	session := sessionFromOpenCode(ocSession, stats, userMsgCount, sessionStatus)
	messages := typedMessagesFromUntyped(pagedMessages)
	parts := typedPartsFromUntyped(pagedParts)
	typedPhase.EndWithDesc("untyped->typed conversion")

	return &platforms.SessionDetail{
		Session:           session,
		Messages:          messages,
		Parts:             parts,
		TotalMessages:     totalMessages,
		ContextTokenCount: int64(stats.contextTokenCount),
		DefaultAgent:      defaults.Agent,
		DefaultModel:      defaults.Model,
	}, true
}

// sessionFromOpenCode builds a typed *db.Session from the OpenCode
// /session/{id} response. Fields absent from the upstream payload end
// up at their zero value / nil — matching the DB-path behaviour for
// the same session.
func sessionFromOpenCode(oc map[string]interface{}, stats messageStats, userMsgCount int, status string) *db.Session {
	timeMap, _ := oc["time"].(map[string]interface{})
	summaryMap, _ := oc["summary"].(map[string]interface{})

	intPtr := func(m map[string]interface{}, key string) *int {
		if m == nil {
			return nil
		}
		v, ok := m[key].(float64)
		if !ok {
			return nil
		}
		n := int(v)
		return &n
	}
	strPtr := func(m map[string]interface{}, key string) *string {
		if m == nil {
			return nil
		}
		v, ok := m[key].(string)
		if !ok || v == "" {
			return nil
		}
		return &v
	}
	strField := func(m map[string]interface{}, key string) string {
		if v, ok := m[key].(string); ok {
			return v
		}
		return ""
	}
	int64Field := func(m map[string]interface{}, key string) int64 {
		if m == nil {
			return 0
		}
		if v, ok := m[key].(float64); ok {
			return int64(v)
		}
		return 0
	}

	timeCreated := int64Field(timeMap, "created")
	timeUpdated := int64Field(timeMap, "updated")

	return &db.Session{
		ID:                strField(oc, "id"),
		Platform:          string(PlatformID),
		ProjectID:         strField(oc, "projectID"),
		Title:             strField(oc, "title"),
		Directory:         strField(oc, "directory"),
		TimeCreated:       timeCreated,
		TimeUpdated:       timeUpdated,
		SummaryAdditions:  intPtr(summaryMap, "additions"),
		SummaryDeletions:  intPtr(summaryMap, "deletions"),
		SummaryFiles:      intPtr(summaryMap, "files"),
		ShareURL:          strPtr(oc, "shareURL"),
		MessageCount:      userMsgCount,
		DurationMs:        stats.durationMs,
		TotalInputTokens:  int64(stats.totalInputTokens),
		TotalOutputTokens: int64(stats.totalOutputTokens),
		TotalCost:         stats.totalCost,
		Status:            status,
		LiveConnection:    true,
	}
}

// typedMessagesFromUntyped re-encodes the `data` map of each untyped
// message into a json.RawMessage, producing a typed db.Message that
// marshals identically to a message read from SQLite.
func typedMessagesFromUntyped(untyped []map[string]interface{}) []db.Message {
	if len(untyped) == 0 {
		return nil
	}
	out := make([]db.Message, 0, len(untyped))
	for _, m := range untyped {
		id, _ := m["id"].(string)
		sid, _ := m["sessionId"].(string)
		var timeCreated int64
		switch v := m["timeCreated"].(type) {
		case int64:
			timeCreated = v
		case float64:
			timeCreated = int64(v)
		}
		var raw json.RawMessage
		if data, ok := m["data"]; ok {
			if bs, err := json.Marshal(data); err == nil {
				raw = bs
			}
		}
		out = append(out, db.Message{
			ID:          id,
			SessionID:   sid,
			TimeCreated: timeCreated,
			Data:        raw,
		})
	}
	return out
}

// typedPartsFromUntyped re-encodes the `data` map of each untyped part
// into a json.RawMessage, producing a typed db.Part.
func typedPartsFromUntyped(untyped []map[string]interface{}) []db.Part {
	if len(untyped) == 0 {
		return nil
	}
	out := make([]db.Part, 0, len(untyped))
	for _, p := range untyped {
		id, _ := p["id"].(string)
		mid, _ := p["messageId"].(string)
		sid, _ := p["sessionId"].(string)
		var raw json.RawMessage
		if data, ok := p["data"]; ok {
			if bs, err := json.Marshal(data); err == nil {
				raw = bs
			}
		}
		out = append(out, db.Part{
			ID:        id,
			MessageID: mid,
			SessionID: sid,
			Data:      raw,
		})
	}
	return out
}
