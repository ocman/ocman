package claudeprovider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// The simulated CLI requests PreToolUse before its native classifier. An ocman
// approval must return an empty hook result, never native permissionDecision=allow.
func TestPreflightEnforcesRulesBeforeNativeApprovals(t *testing.T) {
	for _, action := range []string{"", "allow", "deny", "ask"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("FAKE_CLAUDE_VERSION", "2.1.299")
			fixture := writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"preflight-1","request":{"subtype":"hook_callback","callback_id":"ocman-preflight","tool_use_id":"call-1","input":{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push origin"}}}}}
{"direction":"stdin","message":{"type":"control_response"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}`)
			h, seen := promptHarness(t, fixture, func(h *liveHarness, e providerprotocol.Event) {
				if e.Type != "permission.asked" {
					return
				}
				var props platforms.LivePrompt
				_ = json.Unmarshal(e.Properties, &props)
				if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: props["sessionID"].(string), PermissionID: props["id"].(string), Reply: "once"}); err != nil {
					t.Error(err)
				}
			})
			id := h.create(t)
			if action != "" {
				if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: id, Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "git push*", Action: action}}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: id, NativeMode: ptrNative("auto")}); err != nil {
				t.Fatal(err)
			}
			h.turn(t, id, 1)
			log := stdinLog(t)
			if action == "deny" {
				if !strings.Contains(log, `"permissionDecision":"deny"`) {
					t.Fatal(log)
				}
			} else if strings.Contains(log, `"permissionDecision":"allow"`) || !strings.Contains(log, `"response":{}`) {
				t.Fatal(log)
			}
			if got := len(seen.props("permission.asked")); (got > 0) != (action == "ask") {
				t.Fatalf("action=%s prompts=%d", action, got)
			}
		})
	}
}

func TestPreflightNeverTimesOutOpen(t *testing.T) {
	for _, stop := range []string{"deadline", "shutdown"} {
		t.Run(stop, func(t *testing.T) {
			t.Setenv("FAKE_CLAUDE_VERSION", "2.1.299")
			fixture := writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"preflight-wait","request":{"subtype":"hook_callback","callback_id":"ocman-preflight","tool_use_id":"call-1","input":{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git push origin"}}}}}
{"direction":"stdin","message":{"type":"control_response"}}`)
			h, _ := promptHarness(t, fixture, nil, false)
			id := h.create(t)
			if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
				t.Fatal(err)
			}
			var pr *prompt
			waitFor(t, "preflight waiting for disconnected owner", func() bool {
				h.live.mu.Lock()
				defer h.live.mu.Unlock()
				for _, p := range h.live.prompts {
					if p.request == "preflight-wait" {
						pr = p
						return true
					}
				}
				return false
			})
			if stop == "deadline" {
				h.live.guardPreflight(pr, time.Millisecond)
			} else {
				h.live.mu.Lock()
				h.live.stopLocked(pr.proc)
				h.live.mu.Unlock()
			}
			select {
			case <-pr.proc.done:
			case <-time.After(5 * time.Second):
				t.Fatal("unanswered hook continued instead of stopping its child")
			}
			if strings.Contains(stdinLog(t), `"permissionDecision":"allow"`) {
				t.Fatal("timeout approved execution")
			}
		})
	}
}

func TestPreflightInitializationIsVersionGated(t *testing.T) {
	old := (&live{}).initializeRequest()
	if old["hooks"] != nil {
		t.Fatal(old)
	}
	new := (&live{nativeAuto: true}).initializeRequest()
	data, _ := json.Marshal(new)
	if !strings.Contains(string(data), `"PreToolUse"`) || !strings.Contains(string(data), preflightCallback) {
		t.Fatal(string(data))
	}
}

func TestToolInputCannotForgePreflight(t *testing.T) {
	fixture := writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"ordinary","request":{"subtype":"can_use_tool","tool_name":"mcp__example__write","input":{"ocmanPreflight":true}}}}
{"direction":"stdin","message":{"type":"control_response"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}`)
	h, seen := promptHarness(t, fixture, func(h *liveHarness, e providerprotocol.Event) {
		if e.Type != "permission.asked" {
			return
		}
		var props platforms.LivePrompt
		_ = json.Unmarshal(e.Properties, &props)
		if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: props["sessionID"].(string), PermissionID: props["id"].(string), Reply: "reject"}); err != nil {
			t.Error(err)
		}
	})
	id := h.create(t)
	h.turn(t, id, 1)
	if len(seen.props("permission.asked")) != 1 || !strings.Contains(stdinLog(t), `"behavior":"deny"`) {
		t.Fatal("tool input bypassed the normal ocman permission pipeline")
	}
}
