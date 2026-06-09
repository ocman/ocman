package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// conversationMarkdown renders a full conversation (session metadata +
// messages + parts) to a self-contained Markdown document. It is the
// single source of truth for the export/share Markdown so the
// authenticated download endpoint and the public share endpoint produce
// identical output.
//
// The rendering is intentionally tolerant: parts whose JSON payload
// can't be parsed, or whose type isn't recognized, are skipped rather
// than aborting the whole export. Purely structural part types
// (step-start, step-finish, snapshot) carry no user-facing content and
// are omitted.
func conversationMarkdown(session *db.Session, messages []db.Message, parts []db.Part) string {
	var b strings.Builder

	writeMarkdownHeader(&b, session)

	// Group parts by their owning message for O(1) lookup while
	// iterating messages in chronological order.
	partsByMessage := make(map[string][]db.Part, len(messages))
	for _, p := range parts {
		partsByMessage[p.MessageID] = append(partsByMessage[p.MessageID], p)
	}
	for _, ps := range partsByMessage {
		sort.SliceStable(ps, func(i, j int) bool {
			return ps[i].TimeCreated < ps[j].TimeCreated
		})
	}

	sorted := make([]db.Message, len(messages))
	copy(sorted, messages)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].TimeCreated < sorted[j].TimeCreated
	})

	for _, msg := range sorted {
		writeMarkdownMessage(&b, msg, partsByMessage[msg.ID])
	}

	return b.String()
}

func writeMarkdownHeader(b *strings.Builder, session *db.Session) {
	title := "Conversation"
	if session != nil && strings.TrimSpace(session.Title) != "" {
		title = strings.TrimSpace(session.Title)
	}
	fmt.Fprintf(b, "# %s\n\n", title)

	if session == nil {
		return
	}
	if session.Directory != "" {
		fmt.Fprintf(b, "- **Directory:** `%s`\n", session.Directory)
	}
	if session.TimeCreated > 0 {
		fmt.Fprintf(b, "- **Created:** %s\n", formatUnixMillis(session.TimeCreated))
	}
	if session.TimeUpdated > 0 {
		fmt.Fprintf(b, "- **Updated:** %s\n", formatUnixMillis(session.TimeUpdated))
	}
	if session.MessageCount > 0 {
		fmt.Fprintf(b, "- **Messages:** %d\n", session.MessageCount)
	}
	if session.TotalInputTokens > 0 || session.TotalOutputTokens > 0 {
		fmt.Fprintf(b, "- **Tokens:** %d in / %d out\n", session.TotalInputTokens, session.TotalOutputTokens)
	}
	if session.TotalCost > 0 {
		fmt.Fprintf(b, "- **Cost:** $%.4f\n", session.TotalCost)
	}
	b.WriteString("\n---\n\n")
}

func writeMarkdownMessage(b *strings.Builder, msg db.Message, parts []db.Part) {
	var data db.MessageData
	_ = json.Unmarshal(msg.Data, &data) // tolerant: zero value on error

	heading := messageHeading(data)
	fmt.Fprintf(b, "## %s\n\n", heading)

	wrote := false
	for _, p := range parts {
		if s := partMarkdown(p); s != "" {
			b.WriteString(s)
			b.WriteString("\n\n")
			wrote = true
		}
	}
	if !wrote {
		b.WriteString("_(no content)_\n\n")
	}
}

// messageHeading builds a human-readable section heading for a message
// from its role and (for assistant turns) the model that produced it.
func messageHeading(data db.MessageData) string {
	switch data.Role {
	case "user":
		return "User"
	case "assistant":
		model := data.ModelID
		if model == "" && data.Model != nil {
			model = data.Model.ModelID
		}
		if model != "" {
			return "Assistant (" + model + ")"
		}
		return "Assistant"
	case "":
		return "Message"
	default:
		// Capitalize the first letter of an unknown role.
		return strings.ToUpper(data.Role[:1]) + data.Role[1:]
	}
}

// partPayload is the subset of an OpenCode part's JSON we render into
// Markdown. Unknown fields are ignored.
type partPayload struct {
	Type     string   `json:"type"`
	Text     string   `json:"text"`
	Tool     string   `json:"tool"`
	Filename string   `json:"filename"`
	Mime     string   `json:"mime"`
	Files    []string `json:"files"`
	State    struct {
		Status string                 `json:"status"`
		Title  string                 `json:"title"`
		Input  map[string]interface{} `json:"input"`
		Output interface{}            `json:"output"`
	} `json:"state"`
}

// partMarkdown renders a single part to Markdown, or "" when the part
// has no user-facing content (structural parts, parse failures).
func partMarkdown(p db.Part) string {
	var pp partPayload
	if err := json.Unmarshal(p.Data, &pp); err != nil {
		return ""
	}

	switch pp.Type {
	case "text":
		return strings.TrimRight(pp.Text, "\n")

	case "reasoning":
		text := strings.TrimRight(pp.Text, "\n")
		if text == "" {
			return ""
		}
		// Render reasoning as a blockquote so it's visually distinct
		// from the assistant's actual reply.
		return "> **Reasoning**\n>\n" + blockquote(text)

	case "tool":
		return toolPartMarkdown(pp)

	case "file":
		name := pp.Filename
		if name == "" {
			name = "file"
		}
		return fmt.Sprintf("📎 Attached file: `%s`", name)

	case "patch":
		if len(pp.Files) == 0 {
			return "_Applied a patch._"
		}
		var sb strings.Builder
		sb.WriteString("**Patch applied to:**\n")
		for _, f := range pp.Files {
			fmt.Fprintf(&sb, "- `%s`\n", f)
		}
		return strings.TrimRight(sb.String(), "\n")

	default:
		// step-start, step-finish, snapshot, compaction, subtask, and
		// any future structural types carry no conversation content.
		return ""
	}
}

// toolPartMarkdown renders a tool invocation: a heading line with the
// tool name + status, the input arguments as a JSON code block, and the
// (string) output as a fenced block when present.
func toolPartMarkdown(pp partPayload) string {
	name := pp.Tool
	if name == "" {
		name = "tool"
	}
	var sb strings.Builder
	heading := "🔧 **" + name + "**"
	if pp.State.Title != "" {
		heading += " — " + pp.State.Title
	}
	if pp.State.Status != "" && pp.State.Status != "completed" {
		heading += " _(" + pp.State.Status + ")_"
	}
	sb.WriteString(heading)
	sb.WriteString("\n")

	if len(pp.State.Input) > 0 {
		if enc, err := json.MarshalIndent(pp.State.Input, "", "  "); err == nil {
			sb.WriteString("\n```json\n")
			sb.Write(enc)
			sb.WriteString("\n```\n")
		}
	}

	if out := toolOutputString(pp.State.Output); out != "" {
		sb.WriteString("\n<details>\n<summary>Output</summary>\n\n```\n")
		sb.WriteString(out)
		sb.WriteString("\n```\n\n</details>\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// toolOutputString coerces a tool's output field (which may be a string
// or a structured value) into a printable string. Returns "" for nil.
func toolOutputString(out interface{}) string {
	switch v := out.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimRight(v, "\n")
	default:
		if enc, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(enc)
		}
		return fmt.Sprintf("%v", v)
	}
}

// blockquote prefixes every line of s with "> " so multi-line content
// renders as a single Markdown blockquote.
func blockquote(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n")
}

// formatUnixMillis renders a Unix-ms timestamp as an RFC3339 string in
// local time. Returns "" for non-positive input.
func formatUnixMillis(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05 MST")
}
