package opencode

import (
	"encoding/json"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
)

const maxOutputLen = 200000

// truncatePartOutput limits the size of tool call outputs and large text
// in a part to prevent massive responses.
func truncatePartOutput(part map[string]interface{}) {
	if text, ok := part["text"].(string); ok && len(text) > maxOutputLen {
		part["text"] = text[:maxOutputLen] + "\n... (truncated)"
	}

	state, ok := part["state"].(map[string]interface{})
	if !ok {
		return
	}
	if output, ok := state["output"].(string); ok && len(output) > maxOutputLen {
		state["output"] = output[:maxOutputLen] + "\n... (truncated)"
	}
	if meta, ok := state["metadata"].(map[string]interface{}); ok {
		if output, ok := meta["output"].(string); ok && len(output) > maxOutputLen {
			meta["output"] = output[:maxOutputLen] + "\n... (truncated)"
		}
	}
}

// convertOpenCodeMessages transforms raw OpenCode API messages into the format
// expected by the frontend (separate messages and parts arrays).
func convertOpenCodeMessages(ocMessages []map[string]interface{}) (
	messages []map[string]interface{},
	parts []map[string]interface{},
) {
	messages = make([]map[string]interface{}, 0, len(ocMessages))
	parts = make([]map[string]interface{}, 0)
	skipped := 0
	defer func() {
		if skipped > 0 {
			log.WithFields(log.Fields{
				"skipped": skipped,
				"total":   len(ocMessages),
			}).Debug("opencode: skipped messages with missing/invalid info")
		}
	}()

	for _, m := range ocMessages {
		info, _ := m["info"].(map[string]interface{})
		if info == nil {
			skipped++
			continue
		}

		timeData, _ := info["time"].(map[string]interface{})
		timeCreated := int64(0)
		if tc, ok := timeData["created"].(float64); ok {
			timeCreated = int64(tc)
		}

		msgID, _ := info["id"].(string)
		msgSessionID, _ := info["sessionID"].(string)

		delete(info, "summary")
		delete(info, "path")

		msg := map[string]interface{}{
			"id":          msgID,
			"sessionId":   msgSessionID,
			"timeCreated": timeCreated,
			"data":        info,
		}
		messages = append(messages, msg)

		if msgParts, ok := m["parts"].([]interface{}); ok {
			userExecutedShell := isSynthesizedTerminal(m)
			for _, p := range msgParts {
				part, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				partType, _ := part["type"].(string)
				if partType == "step-start" || partType == "step-finish" || partType == "snapshot" {
					continue
				}
				if userExecutedShell && partType == "tool" {
					if toolName, _ := part["tool"].(string); toolName == "bash" {
						state, _ := part["state"].(map[string]interface{})
						if state == nil {
							state = map[string]interface{}{}
							part["state"] = state
						}
						metadata, _ := state["metadata"].(map[string]interface{})
						if metadata == nil {
							metadata = map[string]interface{}{}
							state["metadata"] = metadata
						}
						metadata["ocmanUserExecutedShell"] = true
					}
				}
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

// messageStats aggregates token counts, cost, and duration from converted messages.
//
// durationMs is wall-clock from the first to the last message in the
// session (includes idle time waiting for the user between turns).
//
// activeDurationMs is the sum of (time.completed - time.created) across
// assistant messages — i.e. the time the agent was actually working on a
// turn. It excludes the gap between an assistant `completed` timestamp
// and the next user message (user think time, permission prompts answered
// between turns). It does NOT subtract permission waits that occur within
// a single assistant turn, because OpenCode does not persist
// permission.asked/replied timestamps in the historical message log —
// those are only available as live SSE events. Assistant messages still
// in flight (no `time.completed`) are skipped so we don't conflate
// "still working" with "active duration so far".
type messageStats struct {
	totalInputTokens  float64
	totalOutputTokens float64
	totalCost         float64
	durationMs        int64
	activeDurationMs  int64
	contextTokenCount float64
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
		// Accumulate active duration: sum of completed assistant turn
		// durations. Only assistant messages with both timestamps and
		// completed > created contribute.
		if role, _ := info["role"].(string); role == "assistant" {
			if timeBlock, ok := info["time"].(map[string]interface{}); ok {
				created, hasCreated := timeBlock["created"].(float64)
				completed, hasCompleted := timeBlock["completed"].(float64)
				if hasCreated && hasCompleted && completed > created {
					stats.activeDurationMs += int64(completed - created)
				}
			}
		}
	}
	if lastTime > firstTime {
		stats.durationMs = int64(lastTime - firstTime)
	}
	return stats
}

// paginateUntyped applies pagination to a slice of untyped maps.
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

// sessionFromOpenCode builds a typed *db.Session from the OpenCode
// /session/{id} response.
func sessionFromOpenCode(oc map[string]interface{}, stats messageStats, userMsgCount int, status db.SessionStatus) *db.Session {
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
		ActiveDurationMs:  stats.activeDurationMs,
		TotalInputTokens:  int64(stats.totalInputTokens),
		TotalOutputTokens: int64(stats.totalOutputTokens),
		TotalCost:         stats.totalCost,
		Status:            status,
		LiveConnection:    true,
	}
}

// typedMessagesFromUntyped re-encodes the `data` map of each untyped
// message into a json.RawMessage, producing a typed db.Message.
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
