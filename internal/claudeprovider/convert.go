package claudeprovider

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Converter is the single Claude -> OpenCode v1 message/part converter.
// Stored transcripts and live stream-json go through it, so part ids agree:
// a message id is the Claude message.id (assistant) or line uuid (user), and
// part ids are prt_<message id>_<block ordinal>.
type Converter struct {
	SessionID string
	// Children maps a Task/Agent tool_use id to its subagent session id.
	Children map[string]string
	Messages []db.Message
	Parts    []db.Part
	cur      *group
}

type group struct {
	info  map[string]any
	parts []map[string]any
	times []int64
	tools map[string]map[string]any
	block int
}

// Add converts one JSONL record. Malformed lines are skipped.
func (c *Converter) Add(raw []byte) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	current := ""
	if c.cur != nil && c.cur.info["role"] == "assistant" {
		current, _ = c.cur.info["id"].(string)
	}
	ts := l.millis()
	if ts == 0 { // live stream-json assistant lines carry no timestamp
		ts = time.Now().UnixMilli()
	}
	switch classify(&l, current) {
	case startUser:
		c.Finish()
		c.start(groupID(&l), "user", ts)
		for _, b := range l.blocks() {
			c.userBlock(b, ts)
		}
	case startAssistant:
		c.Finish()
		c.start(groupID(&l), "assistant", ts)
		c.assistant(&l, ts)
	case continueGroup:
		if c.cur == nil {
			return
		}
		if l.Type == "assistant" {
			c.assistant(&l, ts)
			return
		}
		for _, b := range l.blocks() {
			c.result(b, ts)
		}
		setCompleted(c.cur.info, ts)
	}
}

func (g *group) find(id string) int {
	for i, p := range g.parts {
		if p["id"] == id {
			return i
		}
	}
	return -1
}

func (c *Converter) start(id, role string, ts int64) {
	c.cur = &group{
		info:  map[string]any{"id": id, "sessionID": c.SessionID, "role": role, "time": map[string]any{"created": ts}},
		tools: map[string]map[string]any{},
	}
}

// Live returns the open message and its parts, for publishing live upserts.
func (c *Converter) Live() (map[string]any, []map[string]any) {
	if c.cur == nil {
		return nil, nil
	}
	return c.cur.info, c.cur.parts
}

// Finish flushes the open message. Call it after the last record.
func (c *Converter) Finish() {
	g := c.cur
	if g == nil {
		return
	}
	c.cur = nil
	id := g.info["id"].(string)
	created := g.info["time"].(map[string]any)["created"].(int64)
	data, _ := json.Marshal(g.info)
	c.Messages = append(c.Messages, db.Message{ID: id, SessionID: c.SessionID, TimeCreated: created, Data: data})
	for i, p := range g.parts {
		data, _ := json.Marshal(p)
		c.Parts = append(c.Parts, db.Part{ID: p["id"].(string), MessageID: id, SessionID: c.SessionID, TimeCreated: g.times[i], Data: data})
	}
}

func (c *Converter) part(fields map[string]any, ts int64) map[string]any {
	g := c.cur
	id := g.info["id"].(string)
	fields["id"] = partID(id, g.block)
	fields["messageID"] = id
	fields["sessionID"] = c.SessionID
	g.block++
	if i := g.find(fields["id"].(string)); i >= 0 { // replaces a streamed partial
		g.parts[i], g.times[i] = fields, ts
		return fields
	}
	g.parts = append(g.parts, fields)
	g.times = append(g.times, ts)
	return fields
}

// partID is the one part id scheme: a content block's ordinal in its message,
// which is also the stream's content_block index.
func partID(message string, ordinal int) string { return fmt.Sprintf("prt_%s_%04d", message, ordinal) }

func (c *Converter) userBlock(b block, ts int64) {
	switch b.Type {
	case "text":
		c.part(map[string]any{"type": "text", "text": b.Text}, ts)
	case "image":
		c.part(map[string]any{"type": "file", "mime": b.Source.MediaType, "url": "data:" + b.Source.MediaType + ";base64," + b.Source.Data}, ts)
	default:
		c.cur.block++
	}
}

func (c *Converter) assistant(l *line, ts int64) {
	info := c.cur.info
	info["providerID"], info["modelID"] = "anthropic", l.Message.Model
	info["mode"], info["agent"], info["cost"] = "", "", 0.0
	u := l.Message.Usage // repeated on every line of one API message
	info["tokens"] = map[string]any{"input": u.Input, "output": u.Output, "reasoning": 0,
		"cache": map[string]any{"read": u.CacheRead, "write": u.CacheCreation}}
	if f := finish(l.Message.StopReason); f != "" {
		info["finish"] = f
	}
	setCompleted(info, ts)
	for _, b := range l.blocks() {
		span := map[string]any{"start": ts, "end": ts}
		switch b.Type {
		case "text":
			if l.APIError {
				info["error"] = map[string]any{"name": "APIError", "data": map[string]any{"message": b.Text}}
			}
			c.part(map[string]any{"type": "text", "text": b.Text, "time": span}, ts)
		case "thinking":
			if strings.TrimSpace(b.Thinking) == "" {
				c.cur.block++ // signature-only breadcrumb; keep later ids stable
				continue
			}
			c.part(map[string]any{"type": "reasoning", "text": b.Thinking, "time": span}, ts)
		case "tool_use":
			c.toolUse(b, ts)
		default:
			c.cur.block++
		}
	}
}

func (c *Converter) toolUse(b block, ts int64) {
	var input map[string]any
	_ = json.Unmarshal(b.Input, &input)
	if input == nil {
		input = map[string]any{}
	}
	name := toolName(b.Name)
	metadata := map[string]any{}
	if child := c.Children[b.ID]; child != "" {
		metadata["sessionId"] = child
	}
	if name != "task" {
		input = v1Input(input)
	}
	state := map[string]any{"status": "running", "input": input, "title": toolTitle(input),
		"metadata": metadata, "time": map[string]any{"start": ts}}
	c.cur.tools[b.ID] = state
	c.part(map[string]any{"type": "tool", "tool": name, "callID": b.ID, "state": state}, ts)
}

func (c *Converter) result(b block, ts int64) {
	state := c.cur.tools[b.ToolUseID]
	if b.Type != "tool_result" || state == nil {
		return
	}
	output := flatten(b.Content)
	state["status"], state["output"] = "completed", output
	if b.IsError {
		state["status"], state["error"] = "error", output
	}
	state["time"].(map[string]any)["end"] = ts
	state["metadata"].(map[string]any)["output"] = output
}

func setCompleted(info map[string]any, ts int64) {
	info["time"].(map[string]any)["completed"] = ts
}

// toolName maps Claude tool names to the lowercase v1 names the UI knows.
// MCP tools (mcp__server__tool) keep their name.
func toolName(name string) string {
	if isTaskTool(name) {
		return "task"
	}
	if strings.HasPrefix(name, "mcp__") {
		return name
	}
	return strings.ToLower(name)
}

var v1Keys = map[string]string{"file_path": "filePath", "old_string": "oldString", "new_string": "newString", "replace_all": "replaceAll"}

func v1Input(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if v1, ok := v1Keys[k]; ok {
			k = v1
		}
		out[k] = v
	}
	return out
}

func toolTitle(input map[string]any) string {
	for _, k := range []string{"description", "filePath", "command", "pattern", "url"} {
		if s, ok := input[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// flatten turns a tool_result's string-or-blocks content into text.
func flatten(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return string(content)
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return strings.Join(out, "\n")
}
