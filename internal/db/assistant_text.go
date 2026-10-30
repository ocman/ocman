package db

import (
	"encoding/json"
	"sort"
	"strings"
)

// LatestAssistantText returns the newest assistant message's ID and its
// concatenated text parts. Reasoning, tool and file parts are dropped.
func LatestAssistantText(messages []Message, parts []Part) (string, string) {
	return latestAssistantText(messages, parts, false)
}

// LatestAssistantTextVerbatim concatenates the newest assistant's text parts
// without trimming or inserting separators, for structured session results.
func LatestAssistantTextVerbatim(messages []Message, parts []Part) (string, string) {
	return latestAssistantText(messages, parts, true)
}

func latestAssistantText(messages []Message, parts []Part, verbatim bool) (string, string) {
	var latest Message
	for _, message := range messages {
		var data MessageData
		if json.Unmarshal(message.Data, &data) != nil || data.Role != "assistant" {
			continue
		}
		if latest.ID == "" || message.TimeCreated > latest.TimeCreated ||
			(message.TimeCreated == latest.TimeCreated && message.ID > latest.ID) {
			latest = message
		}
	}
	if latest.ID == "" {
		return "", ""
	}
	selected := make([]Part, 0, len(parts))
	for _, part := range parts {
		if part.MessageID == latest.ID {
			selected = append(selected, part)
		}
	}
	// Stable, with no ID tiebreaker: the live adapter leaves TimeCreated zero,
	// so equal timestamps must keep the adapter's arrival order.
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].TimeCreated < selected[j].TimeCreated })
	var text []string
	for _, part := range selected {
		var payload struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(part.Data, &payload) == nil && payload.Type == "text" {
			if verbatim {
				text = append(text, payload.Text)
			} else if strings.TrimSpace(payload.Text) != "" {
				text = append(text, strings.TrimRight(payload.Text, "\n"))
			}
		}
	}
	if verbatim {
		return latest.ID, strings.Join(text, "")
	}
	return latest.ID, strings.Join(text, "\n\n")
}
