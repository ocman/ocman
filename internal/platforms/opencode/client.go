package opencode

import (
	"bytes"
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

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// rePortSuffix matches a port number at the end of a string (e.g. ":4096").
var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

// openCodeClient is an HTTP client with a reasonable timeout for API calls
// to local OpenCode instances.
var openCodeClient = &http.Client{
	Timeout: 10 * time.Second,
}

// limitedReader wraps a byte slice in a reader for HTTP request bodies.
func limitedReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}

// --- Port discovery with TTL cache ---

// portCache holds cached port discovery results.
var portCache struct {
	mu      sync.Mutex
	ports   map[string]string
	updated time.Time
}

const portCacheTTL = 3 * time.Second

// discoverOpenCodePorts returns a map of directory -> port for all running
// OpenCode instances that are listening on TCP ports.
// Results are cached for a few seconds to avoid calling lsof on every request.
func discoverOpenCodePorts() map[string]string {
	portCache.mu.Lock()
	defer portCache.mu.Unlock()

	if time.Since(portCache.updated) < portCacheTTL && portCache.ports != nil {
		return copyMap(portCache.ports)
	}

	result := discoverOpenCodePortsUncached()
	portCache.ports = result
	portCache.updated = time.Now()
	return copyMap(result)
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

	// Resolve each candidate's cwd.
	for _, c := range candidates {
		cwdOut, err := exec.Command("lsof", "-a", "-p", c.pid, "-d", "cwd", "-F", "n").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(cwdOut), "\n") {
			if strings.HasPrefix(line, "n/") {
				result[line[1:]] = c.port
				break
			}
		}
	}

	return result
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory.
// If the directory is not found in the cached result, the cache is invalidated
// and a fresh lookup is performed before giving up.
func discoverOpenCodePort(directory string) string {
	if port := discoverOpenCodePorts()[directory]; port != "" {
		return port
	}

	// Cache may be stale — force a fresh lookup.
	portCache.mu.Lock()
	portCache.ports = nil
	portCache.mu.Unlock()

	return discoverOpenCodePorts()[directory]
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

// fetchPromptSessionIDs performs the actual HTTP call and returns the set of
// session IDs mentioned in the JSON array response.
func fetchPromptSessionIDs(port, path string) map[string]bool {
	result := map[string]bool{}
	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return result
	}
	var items []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
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

const maxOutputLen = 10000

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
func fetchOpenCodeSession(port, sessionID string) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://127.0.0.1:%s/session/%s", port, sessionID)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("session API returned %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	return result, nil
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
// variants, etc. — none of that matters for selection, so we strip it
// server-side to keep the frontend response small.
type OpenCodeProviderModel struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
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
func fetchOpenCodeProviders(port string) (OpenCodeProvidersResponse, bool) {
	var empty OpenCodeProvidersResponse
	url := fmt.Sprintf("http://127.0.0.1:%s/provider", port)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return empty, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return empty, false
	}
	var parsed OpenCodeProvidersResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return empty, false
	}
	return parsed, true
}

// fetchOpenCodeMessages fetches messages for a session from the OpenCode HTTP API.
func fetchOpenCodeMessages(port, sessionID string) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("http://127.0.0.1:%s/session/%s/message", port, sessionID)
	resp, err := openCodeClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching messages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("messages API returned %d", resp.StatusCode)
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
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
func (a *Adapter) fetchSessionFromOpenCode(sessionID string, limit, offset int) (*platforms.SessionDetail, bool) {
	if a.db == nil {
		return nil, false
	}
	// First get session from DB to find the directory.
	dbSession, err := a.db.GetSession(sessionID)
	if err != nil {
		return nil, false
	}

	port := discoverOpenCodePort(dbSession.Directory)
	if port == "" {
		return nil, false
	}

	// Fetch session detail and messages in parallel.
	var ocSession map[string]interface{}
	var ocMessages []map[string]interface{}
	var sessionErr, messagesErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ocSession, sessionErr = fetchOpenCodeSession(port, sessionID)
	}()
	go func() {
		defer wg.Done()
		ocMessages, messagesErr = fetchOpenCodeMessages(port, sessionID)
	}()
	wg.Wait()

	if sessionErr != nil || messagesErr != nil || ocSession == nil {
		return nil, false
	}

	// Untyped conversion (preserves every OpenCode-specific data key
	// under the message/part .data map). We then re-encode .data into
	// json.RawMessage for the typed Message/Part shape.
	untypedMessages, untypedParts := convertOpenCodeMessages(ocMessages)
	stats := computeMessageStats(untypedMessages)
	totalMessages := len(untypedMessages)
	pagedMessages, pagedMsgIDs := paginateUntyped(untypedMessages, limit, offset)
	pagedParts := filterPartsUntyped(untypedParts, pagedMsgIDs)

	defaults, err := a.db.GetSessionDefaults(sessionID, dbSession.Directory)
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

	session := sessionFromOpenCode(ocSession, stats, userMsgCount, sessionStatus)
	messages := typedMessagesFromUntyped(pagedMessages)
	parts := typedPartsFromUntyped(pagedParts)

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
