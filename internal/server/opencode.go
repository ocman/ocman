package server

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
		return portCache.ports
	}

	result := discoverOpenCodePortsUncached()
	portCache.ports = result
	portCache.updated = time.Now()
	return result
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

// fetchSessionFromOpenCode tries to get session data from the OpenCode HTTP API.
// Returns the response data and true if successful, or nil and false if not available.
func (s *Server) fetchSessionFromOpenCode(sessionID string, limit, offset int) (map[string]interface{}, bool) {
	// First get session from DB to find the directory
	session, err := s.db.GetSession(sessionID)
	if err != nil {
		return nil, false
	}

	port := discoverOpenCodePort(session.Directory)
	if port == "" {
		return nil, false
	}

	// Fetch session detail and messages in parallel
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

	// Convert to our format
	messages, parts := convertOpenCodeMessages(ocMessages)
	stats := computeMessageStats(messages)

	// Apply pagination
	totalMessages := len(messages)
	pagedMessages, pagedMsgIDs := paginateUntyped(messages, limit, offset)
	pagedParts := filterPartsUntyped(parts, pagedMsgIDs)

	// Build session object in our format
	timeMap, _ := ocSession["time"].(map[string]interface{})
	summaryMap, _ := ocSession["summary"].(map[string]interface{})
	defaults, err := s.db.GetSessionDefaults(sessionID, session.Directory)
	if err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "error": err}).Warn("fetching session defaults")
	}

	// Determine status from the last message using shared logic.
	sessionStatus := "done"
	if len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		if info, ok := lastMsg["data"].(map[string]interface{}); ok {
			role, _ := info["role"].(string)
			finish, _ := info["finish"].(string)
			lastErr := ""
			if _, hasError := info["error"]; hasError {
				lastErr = "true" // non-empty signals an error is present
			}
			sessionStatus = db.InferSessionStatus(role, finish, lastErr)
		}
	}

	// Count user messages for messageCount
	userMsgCount := 0
	for _, m := range messages {
		if info, ok := m["data"].(map[string]interface{}); ok {
			if role, ok := info["role"].(string); ok && role == "user" {
				userMsgCount++
			}
		}
	}

	result := map[string]interface{}{
		"session": map[string]interface{}{
			"id":                ocSession["id"],
			"projectId":         ocSession["projectID"],
			"title":             ocSession["title"],
			"directory":         ocSession["directory"],
			"timeCreated":       timeMap["created"],
			"timeUpdated":       timeMap["updated"],
			"summaryAdditions":  summaryMap["additions"],
			"summaryDeletions":  summaryMap["deletions"],
			"summaryFiles":      summaryMap["files"],
			"messageCount":      userMsgCount,
			"durationMs":        stats.durationMs,
			"totalInputTokens":  int64(stats.totalInputTokens),
			"totalOutputTokens": int64(stats.totalOutputTokens),
			"totalCost":         stats.totalCost,
			"contextTokenCount": int64(stats.contextTokenCount),
			"hasPort":           true,
			"status":            sessionStatus,
		},
		"messages":      pagedMessages,
		"parts":         pagedParts,
		"totalMessages": totalMessages,
		"defaultAgent":  defaults.Agent,
		"defaultModel":  defaults.Model,
	}

	return result, true
}
