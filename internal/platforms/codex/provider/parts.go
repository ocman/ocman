package codexprovider

import (
	"encoding/json"
	"regexp"
	"strings"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// ItemPart converts one reply item into a v1 part without ids. Items ocman
// does not render (context compaction, review markers, unknown future types)
// return nil.
func ItemPart(it codexappserver.Item, ts int64) map[string]any {
	span := map[string]any{"start": ts, "end": ts}
	if it.CompletedAtMs > 0 {
		span["end"] = it.CompletedAtMs
	}
	switch it.Type {
	case codexappserver.ItemAgentMessage, codexappserver.ItemPlan:
		return map[string]any{"type": "text", "text": it.Text, "time": span}
	case codexappserver.ItemReasoning:
		text := strings.Join(it.Summary, "\n\n")
		if strings.TrimSpace(text) == "" {
			text = strings.Join(it.ReasoningContent, "\n\n")
		}
		return map[string]any{"type": "reasoning", "text": text, "time": span}
	case codexappserver.ItemCommandExecution:
		return commandPart(it, ts)
	case codexappserver.ItemFileChange:
		part := toolPart(it, "apply_patch", map[string]any{"patchText": Patch(it.Changes)}, summarizeChanges(it.Changes), "", ts)
		part["state"].(map[string]any)["metadata"].(map[string]any)["files"] = fileEntries(it.Changes)
		return part
	case codexappserver.ItemContextCompaction:
		return map[string]any{"type": "text", "text": "Conversation compacted.", "time": span}
	case codexappserver.ItemMcpToolCall:
		return toolPart(it, it.Server+"_"+it.Tool, arguments(it.Arguments), it.Tool, mcpOutput(it.Result), ts)
	case codexappserver.ItemDynamicToolCall:
		return toolPart(it, it.Tool, arguments(it.Arguments), it.Tool, "", ts)
	case codexappserver.ItemWebSearch:
		return toolPart(it, "websearch", map[string]any{"query": it.Query}, it.Query, "", ts)
	case codexappserver.ItemImageView:
		return toolPart(it, "read", map[string]any{"filePath": it.Path}, baseName(it.Path), "", ts)
	}
	return nil
}

func commandPart(it codexappserver.Item, ts int64) map[string]any {
	output := ""
	if it.AggregatedOutput != nil {
		output = *it.AggregatedOutput
	}
	command := unwrapShell(it.Command)
	part := toolPart(it, "bash", map[string]any{"command": command, "description": ""}, command, output, ts)
	metadata := part["state"].(map[string]any)["metadata"].(map[string]any)
	if it.ExitCode != nil {
		metadata["exit"] = *it.ExitCode
	}
	if it.Source == "userShell" {
		metadata["ocmanUserExecutedShell"] = true // a `!` command, not the model's
	}
	return part
}

// toolPart builds a v1 tool part. Items without a status (web search, image
// view) are complete once they exist.
func toolPart(it codexappserver.Item, tool string, input map[string]any, title, output string, ts int64) map[string]any {
	end := ts
	if it.CompletedAtMs > 0 {
		end = it.CompletedAtMs
	}
	state := map[string]any{"input": input, "title": title, "metadata": map[string]any{}, "time": map[string]any{"start": ts}}
	switch it.Status {
	case "inProgress":
		state["status"] = "running"
	case "failed", "declined":
		msg := output
		if it.ToolError != nil && it.ToolError.Message != "" {
			msg = it.ToolError.Message
		}
		if it.Status == "declined" && msg == "" {
			msg = "Declined"
		}
		state["status"], state["error"] = "error", msg
		state["time"].(map[string]any)["end"] = end
	default:
		state["status"], state["output"] = "completed", output
		state["metadata"].(map[string]any)["output"] = output
		state["time"].(map[string]any)["end"] = end
	}
	return map[string]any{"type": "tool", "tool": tool, "callID": it.ID, "state": state}
}

func arguments(raw json.RawMessage) map[string]any {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil || in == nil {
		in = map[string]any{}
		if len(raw) > 0 && string(raw) != "null" {
			in["arguments"] = string(raw)
		}
	}
	return in
}

// mcpOutput joins the text blocks of an MCP CallToolResult.
func mcpOutput(raw json.RawMessage) string {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	var out []string
	for _, b := range result.Content {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return strings.Join(out, "\n")
}

func summarizeChanges(changes []codexappserver.FileUpdate) string {
	if len(changes) == 1 {
		return changes[0].Path
	}
	paths := make([]string, 0, len(changes))
	for _, ch := range changes {
		paths = append(paths, baseName(ch.Path))
	}
	return strings.Join(paths, ", ")
}

// Patch renders file changes in Codex's apply_patch format, which the
// dashboard already displays. Additions and deletions carry the file content;
// updates carry a unified diff whose file headers are dropped.
func Patch(changes []codexappserver.FileUpdate) string {
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")
	for _, ch := range changes {
		switch ch.Kind.Type {
		case "add":
			b.WriteString("*** Add File: " + ch.Path + "\n")
			for _, line := range lines(ch.Diff) {
				b.WriteString("+" + line + "\n")
			}
		case "delete":
			b.WriteString("*** Delete File: " + ch.Path + "\n")
		default:
			b.WriteString("*** Update File: " + ch.Path + "\n")
			if ch.Kind.MovePath != "" {
				b.WriteString("*** Move to: " + ch.Kind.MovePath + "\n")
			}
			inHunk := false
			for _, line := range lines(ch.Diff) {
				// Hunk lines such as a deleted "-- x" look like file headers.
				inHunk = inHunk || strings.HasPrefix(line, "@@")
				if !inHunk && isDiffHeader(line) {
					continue
				}
				b.WriteString(line + "\n")
			}
		}
	}
	b.WriteString("*** End Patch")
	return b.String()
}

func isDiffHeader(line string) bool {
	return strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") ||
		strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ")
}

func lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// shellWrapper matches how Codex runs commands: `<shell> -lc <script>`.
var shellWrapper = regexp.MustCompile(`(?s)^(?:\S*/)?(?:ba|z)?sh -l?c (.+)$`)

// unwrapShell returns the script a wrapped command runs, unquoting a single
// quoted argument. Anything else is returned unchanged.
func unwrapShell(command string) string {
	m := shellWrapper.FindStringSubmatch(command)
	if m == nil {
		return command
	}
	arg := m[1]
	switch {
	case len(arg) >= 2 && arg[0] == '\'' && arg[len(arg)-1] == '\'':
		return strings.ReplaceAll(arg[1:len(arg)-1], `'\''`, "'")
	case len(arg) >= 2 && arg[0] == '"' && arg[len(arg)-1] == '"':
		return doubleQuoteEscapes.Replace(arg[1 : len(arg)-1])
	case strings.ContainsAny(arg, "'\" \\"):
		return command
	}
	return arg
}

var doubleQuoteEscapes = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`")
