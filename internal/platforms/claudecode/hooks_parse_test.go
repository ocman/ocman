package claudecode

import (
	"testing"
	"time"
)

// TestParseHookPayload covers the mapping from a Claude Code hook
// payload (what the CLI writes to stdin when firing a hook) into
// the internal hookEvent. The parser is intentionally tolerant —
// unknown event names produce an ignored=true event, and missing
// fields degrade gracefully rather than erroring.
func TestParseHookPayload(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name      string
		json      string
		wantEvent string // empty = parse should produce an ignored event
		wantSess  string
		wantTool  string
		wantDir   string
		wantIgn   bool
	}{
		{
			name: "UserPromptSubmit produces a prompt event",
			json: `{
				"session_id":"s1",
				"hook_event_name":"UserPromptSubmit",
				"cwd":"/tmp/p",
				"user_prompt":"hi"
			}`,
			wantEvent: "UserPromptSubmit",
			wantSess:  "s1",
			wantDir:   "/tmp/p",
		},
		{
			name: "PreToolUse captures tool name",
			json: `{
				"session_id":"s2",
				"hook_event_name":"PreToolUse",
				"cwd":"/tmp/p",
				"tool_name":"Bash",
				"tool_input":{"command":"ls"}
			}`,
			wantEvent: "PreToolUse",
			wantSess:  "s2",
			wantTool:  "Bash",
			wantDir:   "/tmp/p",
		},
		{
			name: "PostToolUse is parsed",
			json: `{
				"session_id":"s3",
				"hook_event_name":"PostToolUse",
				"tool_name":"Read"
			}`,
			wantEvent: "PostToolUse",
			wantSess:  "s3",
			wantTool:  "Read",
		},
		{
			name: "Stop marks a turn as ending",
			json: `{
				"session_id":"s4",
				"hook_event_name":"Stop",
				"reason":"end_turn"
			}`,
			wantEvent: "Stop",
			wantSess:  "s4",
		},
		{
			name: "SubagentStop is a distinct event",
			json: `{
				"session_id":"s5",
				"hook_event_name":"SubagentStop"
			}`,
			wantEvent: "SubagentStop",
			wantSess:  "s5",
		},
		{
			name: "Notification is parsed",
			json: `{
				"session_id":"s6",
				"hook_event_name":"Notification",
				"message":"needs permission"
			}`,
			wantEvent: "Notification",
			wantSess:  "s6",
		},
		{
			name: "SessionStart is parsed",
			json: `{
				"session_id":"s7",
				"hook_event_name":"SessionStart",
				"cwd":"/tmp/q"
			}`,
			wantEvent: "SessionStart",
			wantSess:  "s7",
			wantDir:   "/tmp/q",
		},
		{
			name: "unknown event_name is ignored",
			json: `{
				"session_id":"sX",
				"hook_event_name":"SomeFutureThing"
			}`,
			wantIgn:  true,
			wantSess: "sX",
		},
		{
			name:    "empty session_id is always ignored",
			json:    `{"hook_event_name":"Stop"}`,
			wantIgn: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := parseHookPayload([]byte(c.json), now)
			if err != nil {
				t.Fatalf("parseHookPayload: %v", err)
			}
			if c.wantIgn {
				if !ev.Ignored {
					t.Errorf("expected Ignored=true, got %+v", ev)
				}
				return
			}
			if ev.Ignored {
				t.Fatalf("unexpected Ignored=true: %+v", ev)
			}
			if ev.EventName != c.wantEvent {
				t.Errorf("EventName = %q, want %q", ev.EventName, c.wantEvent)
			}
			if ev.SessionID != c.wantSess {
				t.Errorf("SessionID = %q, want %q", ev.SessionID, c.wantSess)
			}
			if ev.ToolName != c.wantTool {
				t.Errorf("ToolName = %q, want %q", ev.ToolName, c.wantTool)
			}
			if ev.Cwd != c.wantDir {
				t.Errorf("Cwd = %q, want %q", ev.Cwd, c.wantDir)
			}
			if ev.ReceivedAt.IsZero() {
				t.Error("ReceivedAt should be populated")
			}
		})
	}
}

// TestParseHookPayload_InvalidJSON rejects payloads that don't even
// parse as JSON. Claude Code should never send these, but the HTTP
// handler must not panic on malformed input.
func TestParseHookPayload_InvalidJSON(t *testing.T) {
	_, err := parseHookPayload([]byte("{not json"), time.Now())
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

// TestDeriveSubagentID covers the three important shapes:
//   - subagent transcript path returns the agent id
//   - parent transcript path returns empty
//   - empty input returns empty
func TestDeriveSubagentID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/u/.claude/projects/-home-u-proj/abc/subagents/agent-xyz.jsonl", "xyz"},
		{"/home/u/.claude/projects/-home-u-proj/abc.jsonl", ""},
		{"", ""},
		{"agent-only.jsonl", ""},
	}
	for _, c := range cases {
		if got := deriveSubagentID(c.path); got != c.want {
			t.Errorf("deriveSubagentID(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestSummariseToolInput spot-checks the per-tool-name heuristic; the
// important property is that known tool shapes produce a meaningful
// one-line string and anything malformed returns "".
func TestSummariseToolInput(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"Read", "Read", `{"file_path":"/a/b.go","offset":10}`, "/a/b.go"},
		{"Bash", "Bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"Grep pattern only", "Grep", `{"pattern":"TODO"}`, "TODO"},
		{"Grep pattern + path", "Grep", `{"pattern":"TODO","path":"src"}`, "TODO @ src"},
		{"WebFetch", "WebFetch", `{"url":"https://x.y"}`, "https://x.y"},
		{"unknown tool picks any useful field", "FutureTool", `{"path":"/z"}`, "/z"},
		{"empty input returns empty", "Read", ``, ""},
		{"malformed JSON returns empty", "Read", `{not json`, ""},
		{"newline trimmed to first line", "Bash", "{\"command\":\"echo hi\\nls\"}", "echo hi"},
	}
	for _, c := range cases {
		got := summariseToolInput(c.tool, []byte(c.input))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestHookEventAppliesToolActivity links parseHookPayload and
// toLiveStateDelta end-to-end for a sub-agent-scoped PreToolUse /
// PostToolUse pair in the REAL wire shape Claude Code emits:
// `session_id` + `transcript_path` always reference the parent, and
// `agent_id` is the authoritative sub-agent identifier.
func TestHookEventAppliesToolActivity(t *testing.T) {
	pre := []byte(`{
		"session_id":"sP",
		"hook_event_name":"PreToolUse",
		"tool_name":"Read",
		"tool_input":{"file_path":"/x/y.go"},
		"transcript_path":"/h/.claude/projects/-h-p/sP.jsonl",
		"agent_id":"A",
		"agent_type":"Explore"
	}`)
	ev, err := parseHookPayload(pre, time.Now())
	if err != nil || ev.Ignored {
		t.Fatalf("parseHookPayload PreToolUse: err=%v ignored=%v", err, ev.Ignored)
	}
	if ev.SessionID != "sP" {
		t.Fatalf("SessionID = %q, want sP (parent, not sub-agent)", ev.SessionID)
	}
	d := ev.toLiveStateDelta()
	if d.ToolStart == nil || d.ToolStart.ToolName != "Read" || d.ToolStart.SubagentID != "A" {
		t.Fatalf("PreToolUse delta: %+v", d.ToolStart)
	}
	if d.ToolStart.Summary != "/x/y.go" {
		t.Errorf("summary = %q, want /x/y.go", d.ToolStart.Summary)
	}

	post := []byte(`{
		"session_id":"sP",
		"hook_event_name":"PostToolUse",
		"tool_name":"Read",
		"transcript_path":"/h/.claude/projects/-h-p/sP.jsonl",
		"agent_id":"A"
	}`)
	ev2, _ := parseHookPayload(post, time.Now())
	d2 := ev2.toLiveStateDelta()
	if d2.ToolEnd == nil || d2.ToolEnd.ToolName != "Read" || d2.ToolEnd.SubagentID != "A" {
		t.Fatalf("PostToolUse delta: %+v", d2.ToolEnd)
	}
}

// TestHookEvent_SubagentID_PrefersAgentID covers the preference order
// in hookEvent.subagentID(): AgentID wins over AgentTranscriptPath,
// which wins over TranscriptPath. Regression for the real-world case
// where Claude Code sets `agent_id` and `transcript_path` is the
// PARENT jsonl — we must not mistake the parent path for "no sub-agent".
func TestHookEvent_SubagentID_PrefersAgentID(t *testing.T) {
	cases := []struct {
		name string
		ev   hookEvent
		want string
	}{
		{
			name: "AgentID wins over every path",
			ev: hookEvent{
				AgentID:             "winner",
				AgentTranscriptPath: "/p/parent/subagents/agent-loser1.jsonl",
				TranscriptPath:      "/p/parent/subagents/agent-loser2.jsonl",
			},
			want: "winner",
		},
		{
			name: "falls back to AgentTranscriptPath",
			ev: hookEvent{
				AgentTranscriptPath: "/p/parent/subagents/agent-fromPath.jsonl",
				TranscriptPath:      "/p/parent.jsonl",
			},
			want: "fromPath",
		},
		{
			name: "last-resort TranscriptPath fallback (legacy fixtures)",
			ev: hookEvent{
				TranscriptPath: "/p/parent/subagents/agent-legacy.jsonl",
			},
			want: "legacy",
		},
		{
			name: "parent-level event has no sub-agent id",
			ev: hookEvent{
				TranscriptPath: "/p/parent.jsonl",
			},
			want: "",
		},
		{
			name: "zero-value event has no sub-agent id",
			ev:   hookEvent{},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.ev.subagentID(); got != c.want {
				t.Errorf("subagentID() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSubagentStop_WithoutAgentID_IsInert is the critical regression
// for the bug that prompted this fix. A SubagentStop payload without
// `agent_id` / `agent_transcript_path` must produce an inert delta —
// NOT a `SubagentEnd: ""` that would sweep the parent's Task entry
// out of the live-tool list and collapse the UI's live progress view.
func TestSubagentStop_WithoutAgentID_IsInert(t *testing.T) {
	// Real-world wire shape: transcript_path is the PARENT jsonl.
	payload := []byte(`{
		"session_id":"sP",
		"hook_event_name":"SubagentStop",
		"transcript_path":"/h/.claude/projects/-h-p/sP.jsonl"
	}`)
	ev, err := parseHookPayload(payload, time.Now())
	if err != nil || ev.Ignored {
		t.Fatalf("parseHookPayload: err=%v ignored=%v", err, ev.Ignored)
	}
	d := ev.toLiveStateDelta()
	if d.SubagentEnd != "" {
		t.Errorf("SubagentEnd = %q, want empty (inert) when agent_id is absent", d.SubagentEnd)
	}
	// Status must also stay unset — we don't want a phantom status
	// mutation on the parent session.
	if d.Status != "" {
		t.Errorf("Status = %q, want empty (inert)", d.Status)
	}
}

// TestSubagentStop_WithAgentID_RoutesCorrectly is the positive case:
// when the CLI supplies agent_id, SubagentEnd carries that id so the
// cache can wipe just that sub-agent's tool entries.
func TestSubagentStop_WithAgentID_RoutesCorrectly(t *testing.T) {
	payload := []byte(`{
		"session_id":"sP",
		"hook_event_name":"SubagentStop",
		"transcript_path":"/h/.claude/projects/-h-p/sP.jsonl",
		"agent_id":"A",
		"agent_type":"Explore",
		"agent_transcript_path":"/h/.claude/projects/-h-p/sP/subagents/agent-A.jsonl"
	}`)
	ev, _ := parseHookPayload(payload, time.Now())
	d := ev.toLiveStateDelta()
	if d.SubagentEnd != "A" {
		t.Errorf("SubagentEnd = %q, want A", d.SubagentEnd)
	}
}

// TestParentPreToolUse_NoSubagentID ensures a parent-level tool_use
// (no `agent_id`, parent `transcript_path`) is stored with an empty
// SubagentID — i.e. the Task tool_use itself, which runs at the
// parent level.
func TestParentPreToolUse_NoSubagentID(t *testing.T) {
	payload := []byte(`{
		"session_id":"sP",
		"hook_event_name":"PreToolUse",
		"tool_name":"Task",
		"tool_input":{"description":"explore","subagent_type":"general-purpose"},
		"transcript_path":"/h/.claude/projects/-h-p/sP.jsonl"
	}`)
	ev, _ := parseHookPayload(payload, time.Now())
	d := ev.toLiveStateDelta()
	if d.ToolStart == nil {
		t.Fatal("expected ToolStart, got nil")
	}
	if d.ToolStart.SubagentID != "" {
		t.Errorf("parent-level Task got SubagentID %q, want empty", d.ToolStart.SubagentID)
	}
	if d.ToolStart.ToolName != "Task" {
		t.Errorf("ToolName = %q, want Task", d.ToolStart.ToolName)
	}
}

// TestHookEvent_ToLiveStateDelta covers the event -> LiveState
// transition table. The cache applies these deltas to whatever
// current state exists for the session.
func TestHookEvent_ToLiveStateDelta(t *testing.T) {
	cases := []struct {
		name             string
		event            string
		wantStatus       string
		wantPendingPerm  bool
		wantClearPerm    bool // delta instructs cache to clear pendingPermission
		wantPendingQuest bool
	}{
		{"UserPromptSubmit -> busy", "UserPromptSubmit", "busy", false, true, false},
		{"SessionStart -> busy", "SessionStart", "busy", false, false, false},
		{"PreToolUse stays busy", "PreToolUse", "busy", false, false, false},
		{"PostToolUse stays busy", "PostToolUse", "busy", false, false, false},
		{"Stop -> done", "Stop", "done", false, true, false},
		// SubagentStop no longer forces the parent-session status to
		// "done" — a sub-agent finishing is independent of whether
		// the parent is still working. The next Stop / PreToolUse
		// on the parent updates its status. Instead the delta clears
		// the per-sub-agent live-tool list (covered by liveCache tests).
		{"SubagentStop does not force parent status", "SubagentStop", "", false, false, false},
		{"Notification sets pending permission", "Notification", "", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := hookEvent{EventName: c.event, SessionID: "x", ReceivedAt: time.Now()}
			d := ev.toLiveStateDelta()
			if d.Status != c.wantStatus {
				t.Errorf("Status = %q, want %q", d.Status, c.wantStatus)
			}
			if d.PendingPermission != c.wantPendingPerm {
				t.Errorf("PendingPermission = %v, want %v", d.PendingPermission, c.wantPendingPerm)
			}
			if d.ClearPendingPermission != c.wantClearPerm {
				t.Errorf("ClearPendingPermission = %v, want %v", d.ClearPendingPermission, c.wantClearPerm)
			}
		})
	}
}
