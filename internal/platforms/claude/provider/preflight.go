package claudeprovider

import (
	"encoding/json"
	"time"
)

const preflightCallback = "ocman-preflight"

func (l *live) initializeRequest() map[string]any {
	req := sdkInitializeRequest()
	if l.nativeAuto {
		req["hooks"] = map[string]any{"PreToolUse": []any{map[string]any{
			"matcher": nil, "hookCallbackIds": []string{preflightCallback}, "timeout": 3600,
		}}}
	}
	return req
}

// PreToolUse runs before native approvals. Continue never grants permission:
// Claude's deny rules and classifier still decide after ocman's restrictions.
func (l *live) preflight(p *proc, raw []byte) bool {
	var message struct {
		ID      string `json:"request_id"`
		Request struct {
			Subtype   string `json:"subtype"`
			Callback  string `json:"callback_id"`
			ToolUseID string `json:"tool_use_id"`
			Input     struct {
				Event string         `json:"hook_event_name"`
				Tool  string         `json:"tool_name"`
				Input map[string]any `json:"tool_input"`
			} `json:"input"`
		} `json:"request"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Request.Subtype != "hook_callback" {
		return false
	}
	req := message.Request
	if !l.nativeAuto || req.Callback != preflightCallback || req.Input.Event != "PreToolUse" || req.Input.Tool == "" {
		// A malformed enforcement callback must not become a nonblocking hook error.
		_ = p.cmd.Process.Kill()
		return true
	}
	if req.Input.Tool == "AskUserQuestion" {
		_ = l.write(p, map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": message.ID, "response": map[string]any{}}})
		return true
	}
	translated, _ := json.Marshal(map[string]any{"request_id": message.ID, "request": map[string]any{
		"subtype": "can_use_tool", "ocman_preflight": true, "tool_name": req.Input.Tool,
		"input": req.Input.Input, "tool_use_id": req.ToolUseID,
	}})
	l.ask(p, translated)
	l.mu.Lock()
	var pending *prompt
	for _, pr := range l.prompts {
		if pr.proc == p && pr.request == message.ID {
			pending = pr
			break
		}
	}
	l.mu.Unlock()
	if pending != nil {
		go l.guardPreflight(pending, 30*time.Minute)
	}
	return true
}

// Stop the child before Claude's hook timeout, which would otherwise fail open.
// This also bounds a callback waiting while the ocman owner is disconnected.
func (l *live) guardPreflight(pr *prompt, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-pr.resolved:
	case <-pr.proc.done:
	case <-l.done:
	case <-timer.C:
		_ = pr.proc.cmd.Process.Kill()
	}
}

func (l *live) stopPreflightsLocked(p *proc) {
	for _, pr := range l.prompts {
		if pr.proc == p && pr.preflight && pr.reply == nil {
			_ = p.cmd.Process.Kill()
			return
		}
	}
}

func preflightReply(reply string) map[string]any {
	if reply == "reject" {
		return map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "Blocked by ocman permission rules or reviewer.",
		}}
	}
	return map[string]any{} // continue, never bypass native denials
}
