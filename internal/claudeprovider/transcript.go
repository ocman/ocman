// Package claudeprovider serves Claude Code sessions over ocman's provider
// protocol. It reads Claude's JSONL transcripts through a disposable index
// (claude-index.db) and owns the single Claude -> OpenCode v1 converter.
// It talks to ocman only through internal/providerprotocol.
package claudeprovider

import (
	"encoding/json"
	"strings"
	"time"
)

// line is one transcript (or stream-json) record. Unknown fields and types
// are ignored, never rejected: new CLI releases add both.
type line struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	APIError    bool   `json:"isApiErrorMessage"`
	AITitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	Summary     string `json:"summary"`
	Message     struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
		Usage      usage           `json:"usage"`
	} `json:"message"`
}

type usage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    struct {
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

func (l *line) blocks() []block {
	var text string
	if json.Unmarshal(l.Message.Content, &text) == nil {
		if text == "" {
			return nil
		}
		return []block{{Type: "text", Text: text}}
	}
	var blocks []block
	_ = json.Unmarshal(l.Message.Content, &blocks)
	return blocks
}

func (l *line) millis() int64 {
	t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

const (
	skipLine = iota
	startUser
	startAssistant
	continueGroup
)

// classify is the one grouping rule shared by the index (message offsets)
// and the converter, so a page that starts at an indexed offset converts
// exactly like the whole file. An OpenCode message is a user prompt, or one
// Claude API message (its blocks are written one line each, sharing
// message.id) plus the tool results that answer it.
func classify(l *line, current string) int {
	switch l.Type {
	case "assistant":
		if l.Message.ID != "" && l.Message.ID == current {
			return continueGroup
		}
		return startAssistant
	case "user":
		if l.IsMeta {
			return skipLine
		}
		blocks := l.blocks()
		results := 0
		for _, b := range blocks {
			if b.Type == "tool_result" {
				results++
			}
		}
		if results > 0 && results == len(blocks) {
			return continueGroup
		}
		if len(blocks) == 0 {
			return skipLine
		}
		return startUser
	}
	return skipLine
}

func groupID(l *line) string {
	if l.Type == "assistant" && l.Message.ID != "" {
		return l.Message.ID
	}
	return l.UUID
}

// finish maps Claude's stop_reason to OpenCode's finish vocabulary.
func finish(stop string) string {
	switch stop {
	case "":
		return ""
	case "tool_use":
		return "tool-calls"
	case "max_tokens":
		return "length"
	}
	return "stop"
}

// isTaskTool matches both the advertised Task and the measured Agent name.
func isTaskTool(name string) bool { return name == "Task" || name == "Agent" }

// closesTurn reports user text that ends rather than starts a turn: a
// terminal interrupt marker or local slash-command bookkeeping.
func closesTurn(text string) bool {
	return strings.HasPrefix(text, "[Request interrupted by user") || strings.HasPrefix(text, "<local-command-")
}

const maxTitle = 120

func promptTitle(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") || closesTurn(text) {
		return ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if r := []rune(text); len(r) > maxTitle {
		return string(r[:maxTitle]) + "…"
	}
	return text
}
