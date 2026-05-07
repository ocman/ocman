package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleJSONL is a compact fixture mirroring the real Claude Code
// format: the initial file-history-snapshot (no sessionID),
// a meta user event (don't count), a real user event, an assistant
// event with blocks, and an attachment referencing the assistant.
const sampleJSONL = `{"type":"file-history-snapshot","messageId":"e85cf337","snapshot":{"messageId":"e85cf337"},"isSnapshotUpdate":false}
{"parentUuid":null,"type":"user","message":{"role":"user","content":"<local-command-caveat>Caveat: blah</local-command-caveat>"},"isMeta":true,"uuid":"ba3949d4","timestamp":"2026-04-08T22:48:30.562Z","cwd":"/Users/dries/src/proj","sessionId":"S1","gitBranch":"main","version":"2.1"}
{"parentUuid":"ba3949d4","type":"user","message":{"role":"user","content":"First real message\nwith a second line"},"uuid":"u1","timestamp":"2026-04-08T22:49:00.000Z","cwd":"/Users/dries/src/proj","sessionId":"S1","gitBranch":"main","version":"2.1"}
{"parentUuid":"u1","type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"Hello there"}],"model":"claude-sonnet-4-6"},"uuid":"a1","timestamp":"2026-04-08T22:49:01.500Z","cwd":"/Users/dries/src/proj","sessionId":"S1"}
{"parentUuid":"a1","type":"attachment","attachment":{"type":"deferred_tools_delta"},"uuid":"at1","timestamp":"2026-04-08T22:49:02.000Z","cwd":"/Users/dries/src/proj","sessionId":"S1"}
`

func TestParseHead_ExtractsMetadata(t *testing.T) {
	pf, err := parseReader(strings.NewReader(sampleJSONL), parseHead)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if pf.SessionID != "S1" {
		t.Errorf("SessionID = %q, want S1", pf.SessionID)
	}
	if pf.Cwd != "/Users/dries/src/proj" {
		t.Errorf("Cwd = %q, want /Users/dries/src/proj", pf.Cwd)
	}
	if pf.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want main", pf.GitBranch)
	}
	// Only the non-meta user event counts.
	if pf.UserMessageCount != 1 {
		t.Errorf("UserMessageCount = %d, want 1", pf.UserMessageCount)
	}
	// Title is first line of the first non-meta user message.
	if pf.Title != "First real message" {
		t.Errorf("Title = %q, want %q", pf.Title, "First real message")
	}
	// Time fields: first timestamp is the initial snapshot's (absent),
	// so the first event carrying a timestamp is the meta user event.
	if pf.TimeCreated == 0 {
		t.Error("TimeCreated should be populated from the first event with a timestamp")
	}
	if pf.TimeUpdated <= pf.TimeCreated {
		t.Errorf("TimeUpdated (%d) should exceed TimeCreated (%d)", pf.TimeUpdated, pf.TimeCreated)
	}
	// Head mode must not populate Messages/Parts.
	if len(pf.Messages) != 0 {
		t.Errorf("head-mode parse should not populate Messages, got %d", len(pf.Messages))
	}
}

func TestParseFull_PopulatesMessagesAndParts(t *testing.T) {
	pf, err := parseReader(strings.NewReader(sampleJSONL), parseFull)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	// 2 user events + 1 assistant = 3 messages. Attachment and
	// file-history-snapshot are filtered (internal noise).
	if len(pf.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(pf.Messages))
	}

	// Every message must carry an OpenCode-shaped envelope with a
	// role field — the frontend's shared thread renderer filters on
	// data.role directly, so a raw jsonl payload would hide the
	// message entirely.
	for _, m := range pf.Messages {
		var env struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("message %s data is not an object: %v", m.ID, err)
		}
		if env.Role != "user" && env.Role != "assistant" && env.Role != "system" {
			t.Errorf("message %s: role=%q, want user|assistant|system",
				m.ID, env.Role)
		}
	}

	// Assistant message's assistant envelope carries model + provider
	// hints derived from Claude Code's nested message, plus a synthetic
	// finish="stop" so the frontend's queued-message detection (which
	// reads data.finish to decide whether a preceding assistant turn is
	// still streaming) doesn't mark every follow-up user message as
	// "Queued" for Claude Code sessions.
	for _, m := range pf.Messages {
		if m.ID != "a1" {
			continue
		}
		var env struct {
			Role       string `json:"role"`
			ModelID    string `json:"modelID"`
			ProviderID string `json:"providerID"`
			Finish     string `json:"finish"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("assistant envelope: %v", err)
		}
		if env.ModelID != "claude-sonnet-4-6" {
			t.Errorf("assistant modelID = %q, want claude-sonnet-4-6", env.ModelID)
		}
		if env.ProviderID != "anthropic" {
			t.Errorf("assistant providerID = %q, want anthropic", env.ProviderID)
		}
		if env.Finish != "stop" {
			t.Errorf("assistant finish = %q, want \"stop\"", env.Finish)
		}
	}

	// The assistant has exactly one usable content part (the text
	// block; empty-signature thinking is filtered). No attachment
	// parts — those are internal tool-catalog deltas and are
	// deliberately dropped.
	textParts, otherParts := 0, 0
	for _, p := range pf.Parts {
		if p.MessageID != "a1" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(p.Data, &probe); err != nil {
			t.Fatalf("bad part JSON: %v", err)
		}
		if probe.Type == "text" {
			textParts++
		} else {
			otherParts++
		}
	}
	if textParts != 1 {
		t.Errorf("expected 1 text part for assistant message, got %d", textParts)
	}
	if otherParts != 0 {
		t.Errorf("expected 0 non-text parts on a1, got %d", otherParts)
	}
}

// TestNormalizeContentBlock covers the Anthropic-block-to-OpenCode-
// part-shape translation used by extractParts. The shared frontend
// renderer knows text / reasoning / tool / file; it must not see
// raw "thinking" / "tool_use" types.
func TestNormalizeContentBlock(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantType string
		wantKey  string // optional substring that must appear in output JSON
	}{
		{
			name:     "text",
			in:       `{"type":"text","text":"hi"}`,
			wantType: "text",
			wantKey:  `"text":"hi"`,
		},
		{
			name:     "thinking normalises to reasoning",
			in:       `{"type":"thinking","thinking":"a thought"}`,
			wantType: "reasoning",
			wantKey:  `"text":"a thought"`,
		},
		{
			name:     "tool_use normalises to tool with state.input",
			in:       `{"type":"tool_use","name":"bash","input":{"cmd":"ls"}}`,
			wantType: "tool",
			wantKey:  `"tool":"bash"`,
		},
		{
			name:     "tool_result with string content",
			in:       `{"type":"tool_result","tool_use_id":"t1","content":"ok"}`,
			wantType: "tool",
			wantKey:  `"output":"ok"`,
		},
		{
			name:     "image block becomes file part with data url",
			in:       `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}`,
			wantType: "file",
			wantKey:  `"url":"data:image/png;base64,AAAA"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := normalizeContentBlock(json.RawMessage(c.in))
			if out == nil {
				t.Fatalf("normalizeContentBlock returned nil for %s", c.in)
			}
			var probe struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(out, &probe); err != nil {
				t.Fatalf("unmarshal normalised block: %v", err)
			}
			if probe.Type != c.wantType {
				t.Errorf("type = %q, want %q (raw: %s)", probe.Type, c.wantType, out)
			}
			if c.wantKey != "" && !strings.Contains(string(out), c.wantKey) {
				t.Errorf("expected %q in output, got %s", c.wantKey, out)
			}
		})
	}
}

func TestExtractTitle_SkipsXMLWrappers(t *testing.T) {
	// <command-message> wrappers are not useful titles.
	evt := jsonlEvent{Message: []byte(`{"role":"user","content":"<command-message>clear</command-message>"}`)}
	if got := extractTitle(evt); got != "" {
		t.Errorf("expected empty title for wrapper, got %q", got)
	}
}

func TestExtractTitle_TruncatesAtMaxLen(t *testing.T) {
	long := strings.Repeat("a", maxTitleLen+20)
	evt := jsonlEvent{Message: []byte(`{"role":"user","content":` + quoteJSON(long) + `}`)}
	got := extractTitle(evt)
	if got == "" || !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncated title ending with ellipsis, got %q", got)
	}
}

func TestParse_TolerantOfMalformedLines(t *testing.T) {
	bad := "not json at all\n" + sampleJSONL
	pf, err := parseReader(strings.NewReader(bad), parseHead)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if pf.SessionID != "S1" {
		t.Errorf("malformed line should be skipped; expected S1, got %q", pf.SessionID)
	}
}

func TestParseTimestampMs(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = non-zero
	}{
		{"", false},
		{"2026-04-08T22:49:00.000Z", true},
		{"not-a-time", false},
	}
	for _, c := range cases {
		got := parseTimestampMs(c.in)
		if (got != 0) != c.want {
			t.Errorf("parseTimestampMs(%q) = %d, wantNonZero=%v", c.in, got, c.want)
		}
	}
}

// quoteJSON returns s wrapped as a JSON string literal for embedding
// in fixture data. Simplified: assumes s contains no quotes or
// control characters (true for tests that pass ASCII).
func quoteJSON(s string) string {
	return `"` + s + `"`
}

// TestMarkRunningToolUses_FlipsUnmatchedToolUses verifies that a
// tool_use without a matching tool_result is marked "running", while
// a tool_use whose tool_result is present stays "completed".
func TestMarkRunningToolUses_FlipsUnmatchedToolUses(t *testing.T) {
	// Two tool uses: one has a matching tool_result, one doesn't.
	// The full parse should leave the matched one as completed and
	// flip the unmatched one to "running".
	jsonl := `{"type":"assistant","uuid":"a1","sessionId":"S1","timestamp":"2026-04-08T22:49:01.500Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_done","name":"Read","input":{"file_path":"/a"}},{"type":"tool_use","id":"tu_open","name":"Task","input":{"description":"go deep"}}]}}
{"type":"user","uuid":"u2","sessionId":"S1","timestamp":"2026-04-08T22:49:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_done","content":"ok"}]}}
`
	pf, err := parseReader(strings.NewReader(jsonl), parseFull)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}

	var foundRunning, foundCompleted bool
	for _, p := range pf.Parts {
		var probe struct {
			Tool  string `json:"tool"`
			ID    string `json:"id"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		if err := json.Unmarshal(p.Data, &probe); err != nil {
			t.Fatalf("unmarshal part: %v (data=%s)", err, p.Data)
		}
		if probe.Tool == "result" {
			continue
		}
		switch probe.ID {
		case "tu_done":
			if probe.State.Status != "completed" {
				t.Errorf("tu_done status = %q, want completed", probe.State.Status)
			}
			foundCompleted = true
		case "tu_open":
			if probe.State.Status != "running" {
				t.Errorf("tu_open status = %q, want running", probe.State.Status)
			}
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Error("did not find the unmatched tool_use; parse may have dropped it")
	}
	if !foundCompleted {
		t.Error("did not find the matched tool_use")
	}
}

// TestMarkRunningToolUses_CopiesOutputOntoToolUse verifies that the
// tool_result's output text is copied onto the matching tool_use
// part's state.output. This normalises Claude Code's split
// representation into the same shape OpenCode uses so the frontend
// can read state.output without platform-specific branching.
func TestMarkRunningToolUses_CopiesOutputOntoToolUse(t *testing.T) {
	jsonl := `{"type":"assistant","uuid":"a1","sessionId":"S1","timestamp":"2026-04-08T22:49:01.500Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Task","input":{"description":"explore codebase"}},{"type":"tool_use","id":"tu2","name":"Read","input":{"file_path":"/a"}}]}}
{"type":"user","uuid":"u2","sessionId":"S1","timestamp":"2026-04-08T22:49:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"task_id: ses_abc123\n\n<task_result>\nFound 3 files.\n</task_result>"},{"type":"tool_result","tool_use_id":"tu2","content":"file contents here"}]}}
`
	pf, err := parseReader(strings.NewReader(jsonl), parseFull)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}

	for _, p := range pf.Parts {
		var probe struct {
			Tool  string `json:"tool"`
			ID    string `json:"id"`
			State struct {
				Status string `json:"status"`
				Output string `json:"output"`
			} `json:"state"`
		}
		if err := json.Unmarshal(p.Data, &probe); err != nil {
			t.Fatalf("unmarshal part: %v (data=%s)", err, p.Data)
		}
		if probe.Tool == "result" {
			continue
		}
		switch probe.ID {
		case "tu1":
			if probe.State.Status != "completed" {
				t.Errorf("tu1 status = %q, want completed", probe.State.Status)
			}
			if probe.State.Output == "" {
				t.Error("tu1 state.output should be populated from tool_result")
			}
			if !strings.Contains(probe.State.Output, "Found 3 files") {
				t.Errorf("tu1 state.output = %q, want it to contain the task result", probe.State.Output)
			}
		case "tu2":
			if probe.State.Status != "completed" {
				t.Errorf("tu2 status = %q, want completed", probe.State.Status)
			}
			if probe.State.Output != "file contents here" {
				t.Errorf("tu2 state.output = %q, want %q", probe.State.Output, "file contents here")
			}
		}
	}
}

// TestMarkRunningToolUses_EmptyOutputNotCopied verifies that when a
// tool_result has empty output, the tool_use part is not rewritten
// (no spurious empty state.output field).
func TestMarkRunningToolUses_EmptyOutputNotCopied(t *testing.T) {
	jsonl := `{"type":"assistant","uuid":"a1","sessionId":"S1","timestamp":"2026-04-08T22:49:01.500Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"bash","input":{"command":"true"}}]}}
{"type":"user","uuid":"u2","sessionId":"S1","timestamp":"2026-04-08T22:49:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":""}]}}
`
	pf, err := parseReader(strings.NewReader(jsonl), parseFull)
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}

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
		if probe.Tool == "result" || probe.ID != "tu1" {
			continue
		}
		// The original tool_use part should not have been rewritten
		// since the tool_result output was empty.
		if probe.State.Output != "" {
			t.Errorf("tu1 state.output = %q, want empty (no rewrite for empty output)", probe.State.Output)
		}
	}
}
