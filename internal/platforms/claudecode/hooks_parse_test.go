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
		{"SubagentStop -> done", "SubagentStop", "done", false, false, false},
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
