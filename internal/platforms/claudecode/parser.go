package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// parsedFile holds everything we derive from a single <uuid>.jsonl.
// Lives in the cache keyed by (path, mtime, size); every mutation
// means a re-parse.
type parsedFile struct {
	// SessionID is the `sessionId` found on the first event that
	// carries one (file-history-snapshot events don't).
	SessionID string

	// Cwd is the absolute project directory taken from the first
	// event's `cwd` field. Authoritative (see AD-11) — we never
	// reverse the dash-encoding.
	Cwd string

	// GitBranch is the branch extracted from the first event (if any).
	GitBranch string

	// TimeCreated / TimeUpdated are epoch milliseconds derived from
	// the first / last event timestamps.
	TimeCreated int64
	TimeUpdated int64

	// UserMessageCount is the number of non-meta user turns in the
	// session. The "message count" shown in the dashboard matches
	// OpenCode semantics (user turns, not assistant responses).
	UserMessageCount int

	// Title is the first non-meta user message's first line, trimmed
	// to a reasonable length. Used as the session title when Claude
	// Code has no better one.
	Title string

	// Messages / Parts are the converted events ready to be surfaced
	// through the Platform.Session endpoint. Only populated by a
	// full parse; Sessions()-driven head reads leave them nil.
	Messages []db.Message
	Parts    []db.Part
}

// parseMode controls how much of a file we read.
type parseMode int

const (
	// parseHead reads only enough events to populate session-list
	// metadata: sessionId, cwd, first + last timestamps, user
	// message count, title. Does not populate Messages/Parts.
	parseHead parseMode = iota

	// parseFull reads the entire file and populates Messages + Parts
	// for the detail view.
	parseFull
)

// parseFile opens path and parses it in the given mode. The returned
// parsedFile is never nil unless err is non-nil.
func parseFile(path string, mode parseMode) (*parsedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return parseReader(f, mode)
}

// parseReader is the core parse loop, factored out of parseFile so
// tests can feed in synthetic jsonl strings without touching disk.
func parseReader(r io.Reader, mode parseMode) (*parsedFile, error) {
	pf := &parsedFile{}
	scanner := bufio.NewScanner(r)
	// jsonl lines can be large (full tool outputs, attachments).
	// 16 MiB is the biggest single line we've observed in the wild;
	// double that for headroom.
	scanner.Buffer(make([]byte, 0, 1<<16), 32<<20)

	// Track last event's timestamp so we can use it as TimeUpdated.
	var lastTimestampMs int64

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt jsonlEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			// Tolerate malformed lines — Claude Code can crash mid-
			// write. Skipping is better than failing the whole file.
			continue
		}

		// First-event metadata (session-id, cwd, etc.). We take them
		// from whichever event first exposes them, since the initial
		// `file-history-snapshot` doesn't carry session context.
		if pf.SessionID == "" && evt.SessionID != "" {
			pf.SessionID = evt.SessionID
		}
		if pf.Cwd == "" && evt.Cwd != "" {
			pf.Cwd = evt.Cwd
		}
		if pf.GitBranch == "" && evt.GitBranch != "" {
			pf.GitBranch = evt.GitBranch
		}

		ts := parseTimestampMs(evt.Timestamp)
		if ts > 0 {
			if pf.TimeCreated == 0 {
				pf.TimeCreated = ts
			}
			if ts > lastTimestampMs {
				lastTimestampMs = ts
				pf.TimeUpdated = ts
			}
		}

		// Non-meta user messages are the ones that count as
		// "turns" and populate the title.
		if evt.Type == "user" && !evt.IsMeta {
			pf.UserMessageCount++
			if pf.Title == "" {
				pf.Title = extractTitle(evt)
			}
		}

		if mode == parseFull {
			appendConvertedEvent(pf, evt, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	if mode == parseFull {
		markRunningToolUses(pf)
	}

	return pf, nil
}

// markRunningToolUses walks the parsed parts after a full parse and
// flips state.status to "running" for every tool_use that does not
// yet have a matching tool_result. This is the only signal we have,
// from a static jsonl, that a Task (or any other tool) is still in
// flight — the CLI appends tool_result only after the tool returns.
//
// For resolved tool_uses (those with a matching tool_result), the
// tool_result's output is copied onto the tool_use part's
// state.output. This normalises Claude Code's split representation
// (tool_use on the assistant message, tool_result on the user
// message) into the same shape OpenCode uses (output on the tool
// part itself), so the frontend can read state.output without
// platform-specific logic.
//
// The UI uses the running status to render the compact "live" Task
// card and to overlay the live-tool list from the hook-driven cache.
func markRunningToolUses(pf *parsedFile) {
	if pf == nil || len(pf.Parts) == 0 {
		return
	}
	// Collect every tool_use_id referenced by a tool_result part,
	// along with its output text so we can copy it onto the matching
	// tool_use part below.
	type resultInfo struct {
		output string
	}
	resolved := make(map[string]resultInfo, len(pf.Parts))
	for _, p := range pf.Parts {
		var probe struct {
			Tool  string `json:"tool"`
			ID    string `json:"id"`
			State struct {
				Output string `json:"output"`
			} `json:"state"`
		}
		if err := json.Unmarshal(p.Data, &probe); err != nil {
			continue
		}
		if probe.Tool == "result" && probe.ID != "" {
			resolved[probe.ID] = resultInfo{output: probe.State.Output}
		}
	}
	if len(resolved) == len(pf.Parts) {
		// Every part is a result — nothing to mark.
		return
	}
	for i, p := range pf.Parts {
		var probe struct {
			Type  string          `json:"type"`
			Tool  string          `json:"tool"`
			ID    string          `json:"id"`
			State json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(p.Data, &probe); err != nil {
			continue
		}
		if probe.Type != "tool" || probe.Tool == "" || probe.Tool == "result" {
			continue
		}
		if probe.ID == "" {
			continue
		}
		ri, isResolved := resolved[probe.ID]
		if isResolved {
			// Resolved: copy the tool_result output onto this
			// tool_use part so the frontend can read state.output
			// directly, matching the OpenCode data shape.
			if ri.output != "" {
				var state map[string]interface{}
				if len(probe.State) > 0 {
					_ = json.Unmarshal(probe.State, &state)
				}
				if state == nil {
					state = map[string]interface{}{}
				}
				state["output"] = ri.output
				replacement, err := json.Marshal(map[string]interface{}{
					"type":  "tool",
					"tool":  probe.Tool,
					"id":    probe.ID,
					"state": state,
				})
				if err != nil {
					continue
				}
				pf.Parts[i].Data = replacement
			}
			continue
		}
		// Unresolved: rewrite state.status -> "running". Preserve
		// everything else in state verbatim by deserialising into a
		// generic map.
		var state map[string]interface{}
		if len(probe.State) > 0 {
			_ = json.Unmarshal(probe.State, &state)
		}
		if state == nil {
			state = map[string]interface{}{}
		}
		state["status"] = "running"
		replacement, err := json.Marshal(map[string]interface{}{
			"type":  "tool",
			"tool":  probe.Tool,
			"id":    probe.ID,
			"state": state,
		})
		if err != nil {
			continue
		}
		pf.Parts[i].Data = replacement
	}
}

// jsonlEvent is the minimal shape we extract from every line.
// Additional fields are captured opaquely in RawData for round-
// tripping to the frontend in parseFull mode.
type jsonlEvent struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Timestamp   string          `json:"timestamp"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
	Message     json.RawMessage `json:"message"`
	Content     json.RawMessage `json:"content"` // system events use `content` instead of `message`
	Attachment  json.RawMessage `json:"attachment"`
	Snapshot    json.RawMessage `json:"snapshot"`  // file-history-snapshot shape
	MessageID   string          `json:"messageId"` // file-history-snapshot links to a message
}

// parseTimestampMs converts Claude Code's RFC3339Nano timestamps into
// epoch milliseconds. Returns 0 for empty or unparseable input so
// callers can treat "no timestamp yet" as zero.
func parseTimestampMs(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// extractTitle returns a single-line excerpt from a user event,
// trimmed to maxTitleLen runes. Skips the XML-ish wrapper that Claude
// Code inserts for slash commands ("<command-name>/foo</command-name>"
// etc.) — the resulting title from those is empty, which is correct.
func extractTitle(evt jsonlEvent) string {
	// message.content is polymorphic: string or an array of blocks.
	// For titles we only care about the string form of user input.
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(evt.Message, &msg); err != nil {
		return ""
	}
	var text string
	if err := json.Unmarshal(msg.Content, &text); err != nil {
		// Not a plain string — give up rather than try to flatten
		// a content-block array. Phase-4 titles only need the common
		// case.
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Drop local-command metadata wrappers — those aren't real
	// titles.
	if strings.HasPrefix(text, "<local-command-caveat>") ||
		strings.HasPrefix(text, "<command-name>") ||
		strings.HasPrefix(text, "<command-message>") {
		return ""
	}
	// First line only.
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return truncateRunes(text, maxTitleLen)
}

const maxTitleLen = 120

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	// Fast path: bytes == runes for ASCII.
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// appendConvertedEvent turns one jsonl event into zero-or-more
// db.Message / db.Part entries on pf.
//
// The frontend (OcmanRuntimeProvider.convertMessages) reads
// m.data.role directly to decide whether a message renders as a user
// or assistant turn. Claude Code nests role under data.message.role
// rather than at the top level, so we can't pass the raw jsonl event
// through — we'd end up with every message filtered out as neither
// user nor assistant. Instead, each message is wrapped in a minimal
// OpenCode-shaped envelope {role, agent?, model?, providerID?, raw?}
// that is rich enough for the shared thread renderer while preserving
// the original event under .raw for any future platform-specific UI.
//
// Only user / assistant / system events produce messages; attachments
// become parts on their parent message; file-history-snapshots are
// skipped. Parts are emitted in OpenCode's part shape too so that the
// existing text / tool-call / image branches render unchanged.
func appendConvertedEvent(pf *parsedFile, evt jsonlEvent, raw []byte) {
	if evt.Type == "file-history-snapshot" {
		// Filesystem snapshots carry no user-facing content.
		return
	}

	messageID := evt.UUID
	if messageID == "" {
		// Some event types (attachment) reference an existing
		// message by ID instead of introducing a new one. Link
		// them via the parent pointer so the frontend groups
		// them under the right turn.
		messageID = evt.ParentUUID
	}

	timeMs := parseTimestampMs(evt.Timestamp)

	switch evt.Type {
	case "user", "assistant":
		pf.Messages = append(pf.Messages, db.Message{
			ID:          messageID,
			SessionID:   evt.SessionID,
			TimeCreated: timeMs,
			Data:        buildMessageEnvelope(evt.Type, evt, raw),
		})
		// Best-effort part extraction: `message.content` may be an
		// array of blocks (text, thinking, tool_use, ...). Emit one
		// part per block so the frontend can render them
		// individually. For a string `message.content`, emit a
		// single text part.
		extractParts(pf, messageID, evt)
	case "system":
		// System events get represented as a message with role
		// "system" — most are short diagnostic notes. The shared
		// thread filters them out, but we still surface them so
		// consumers (e.g. a future platform-aware debug view) can
		// access them.
		pf.Messages = append(pf.Messages, db.Message{
			ID:          messageID,
			SessionID:   evt.SessionID,
			TimeCreated: timeMs,
			Data:        buildMessageEnvelope("system", evt, raw),
		})
	case "attachment":
		// Claude Code's attachments are internal tool-catalog deltas
		// (deferred_tools_delta) sent to the model, not user-facing
		// content. Dropping them keeps the thread clean; if we ever
		// surface a platform-specific debug view it can re-parse
		// from the raw jsonl file on demand.
		return
	}
}

// buildMessageEnvelope wraps a raw Claude Code jsonl event in the
// OpenCode-shaped message envelope that the frontend's shared
// renderer expects. Only the fields the renderer reads are
// synthesized; everything else is preserved under .raw for
// round-tripping.
//
// For assistant messages we also surface Claude Code's model string
// under modelID so the per-message model badge renders without
// special-casing the platform. Historical assistant messages from
// Claude Code's jsonl are always terminal (the file only contains
// completed turns), so we set `finish: "stop"` to match OpenCode's
// shape — the frontend's queued-message detection reads `finish` to
// decide whether a preceding assistant turn is still streaming.
func buildMessageEnvelope(role string, evt jsonlEvent, raw []byte) json.RawMessage {
	envelope := map[string]interface{}{
		"role": role,
		"raw":  json.RawMessage(raw),
	}
	if role == "assistant" {
		envelope["finish"] = "stop"
		if len(evt.Message) > 0 {
			// Pull the model name out of the nested anthropic-shaped
			// message body. We don't care about anything else — the
			// content blocks are already surfaced as parts.
			var inner struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(evt.Message, &inner); err == nil && inner.Model != "" {
				envelope["modelID"] = inner.Model
				envelope["providerID"] = "anthropic"
			}
		}
	}
	buf, err := json.Marshal(envelope)
	if err != nil {
		// Extremely unlikely (map of JSON-safe values); fall back
		// to the raw event so the message is at least present.
		return json.RawMessage(raw)
	}
	return buf
}

// extractParts emits one db.Part per content block in a user/assistant
// message. Safe to call with any event shape — missing or malformed
// content just produces no parts.
func extractParts(pf *parsedFile, messageID string, evt jsonlEvent) {
	if len(evt.Message) == 0 || messageID == "" {
		return
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(evt.Message, &msg); err != nil {
		return
	}
	if len(msg.Content) == 0 {
		return
	}

	// String content -> single text part.
	var str string
	if err := json.Unmarshal(msg.Content, &str); err == nil {
		if str == "" {
			return
		}
		data, _ := json.Marshal(map[string]interface{}{
			"type": "text",
			"text": str,
		})
		pf.Parts = append(pf.Parts, db.Part{
			ID:        messageID + ":text",
			MessageID: messageID,
			SessionID: evt.SessionID,
			Data:      json.RawMessage(data),
		})
		return
	}

	// Array content -> one part per block, translated from the
	// Anthropic SDK's block types into the OpenCode-shaped part
	// types the shared thread renderer already knows how to display.
	// Skip blocks that are internal artifacts (empty-signature
	// `thinking` entries) since they carry no user-facing content.
	var blocks []json.RawMessage
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}
	for i, block := range blocks {
		if !isUsefulBlock(block) {
			continue
		}
		normalized := normalizeContentBlock(block)
		if normalized == nil {
			continue
		}
		pf.Parts = append(pf.Parts, db.Part{
			ID:        fmt.Sprintf("%s:%d", messageID, i),
			MessageID: messageID,
			SessionID: evt.SessionID,
			Data:      normalized,
		})
	}
}

// normalizeContentBlock maps an Anthropic-SDK content block into the
// OpenCode part shape the frontend renderer understands.
//
// Mapping (see OcmanRuntimeProvider.convertMessages switch):
//   - text          -> {type:"text", text}
//   - thinking      -> {type:"reasoning", text}
//   - tool_use      -> {type:"tool", tool, state:{status, input}}
//   - tool_result   -> {type:"tool", tool:"result", state:{output}}
//   - image         -> {type:"file", mime, url} (data-url)
//
// Unknown block types are passed through as-is so they still
// surface via the renderer's default branch rather than disappearing
// silently. Returns nil to drop the block entirely.
func normalizeContentBlock(block json.RawMessage) json.RawMessage {
	var probe struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		ID        string          `json:"id"`          // tool_use (Anthropic SDK)
		Name      string          `json:"name"`        // tool_use
		Input     json.RawMessage `json:"input"`       // tool_use
		ToolUseID string          `json:"tool_use_id"` // tool_result
		Content   json.RawMessage `json:"content"`     // tool_result (string or blocks)
		Source    json.RawMessage `json:"source"`      // image
	}
	if err := json.Unmarshal(block, &probe); err != nil {
		return block
	}

	switch probe.Type {
	case "text":
		out, _ := json.Marshal(map[string]interface{}{
			"type": "text",
			"text": probe.Text,
		})
		return out
	case "thinking":
		out, _ := json.Marshal(map[string]interface{}{
			"type": "reasoning",
			"text": probe.Thinking,
		})
		return out
	case "tool_use":
		// Mirror OpenCode's tool-part shape: top-level tool name,
		// with input under state.input. Default status="completed"
		// here — a post-pass (markRunningToolUses) downgrades any
		// tool_use without a matching tool_result to "running" so
		// the UI can render a live indicator while work is ongoing.
		out, _ := json.Marshal(map[string]interface{}{
			"type": "tool",
			"tool": probe.Name,
			"id":   probe.ID,
			"state": map[string]interface{}{
				"status": "completed",
				"input":  json.RawMessage(probe.Input),
			},
		})
		return out
	case "tool_result":
		// Tool results live on the *user* side of an Anthropic turn.
		// Represent them as a completed tool part so the renderer's
		// default tool-call branch picks them up. Output can be a
		// plain string or a list of blocks; stringify blocks so the
		// renderer's truncate+display path can cope.
		output := flattenToolResultContent(probe.Content)
		out, _ := json.Marshal(map[string]interface{}{
			"type": "tool",
			"tool": "result",
			"id":   probe.ToolUseID,
			"state": map[string]interface{}{
				"status": "completed",
				"output": output,
			},
		})
		return out
	case "image":
		// source is {type:"base64", media_type:"image/png", data:"..."}.
		var src struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		}
		if err := json.Unmarshal(probe.Source, &src); err != nil || src.Data == "" {
			return nil
		}
		url := "data:" + src.MediaType + ";base64," + src.Data
		out, _ := json.Marshal(map[string]interface{}{
			"type": "file",
			"mime": src.MediaType,
			"url":  url,
		})
		return out
	}
	// Unknown types: pass through so the renderer's default branch
	// still surfaces something rather than dropping silently.
	return block
}

// flattenToolResultContent turns the polymorphic content of a
// tool_result block into a display-friendly string. Anthropic sends
// either a bare string, a list of text/image blocks, or JSON —
// we concatenate text blocks and JSON-encode anything else.
func flattenToolResultContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return str
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(content, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			var probe struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(b, &probe) == nil && probe.Type == "text" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(probe.Text)
				continue
			}
			// Non-text block — keep it raw so the user sees something.
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.Write(b)
		}
		return sb.String()
	}
	// Fall through: content is some JSON we don't recognise —
	// surface it verbatim.
	return string(content)
}

// isUsefulBlock returns true for content blocks that carry user-facing
// content. Empty `thinking` entries (Claude's internal signature-only
// breadcrumbs) are filtered out; tool_use / tool_result / text /
// thinking-with-text are kept.
func isUsefulBlock(block json.RawMessage) bool {
	var probe struct {
		Type     string `json:"type"`
		Thinking string `json:"thinking"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(block, &probe); err != nil {
		return true // preserve on doubt
	}
	if probe.Type == "thinking" && strings.TrimSpace(probe.Thinking) == "" {
		return false
	}
	return true
}
