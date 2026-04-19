package claudecode

import (
	"encoding/json"
	"fmt"
	"time"
)

// hookEvent is the normalised form of a Claude Code hook payload,
// decoupled from the raw JSON shape the CLI posts. Only the fields
// the live-state cache actually consults are kept — anything else is
// discarded intentionally to keep the cache surface small.
//
// The CLI posts the payload documented at:
//
//	https://github.com/anthropics/claude-code/blob/main/plugins/plugin-dev/skills/hook-development/SKILL.md
//
// Common fields: session_id, transcript_path, cwd, permission_mode,
// hook_event_name. Event-specific: tool_name, tool_input, user_prompt,
// reason, message.
type hookEvent struct {
	// EventName is Claude Code's hook_event_name, e.g. UserPromptSubmit.
	// Unknown events set Ignored=true so callers can log + skip cleanly.
	EventName string

	// SessionID is the Claude Code session UUID. Empty sessionID is
	// always treated as Ignored — we can't route it anywhere.
	SessionID string

	// Cwd is the working directory the session was launched in. Used
	// purely for diagnostics / log context; the cache keys on SessionID.
	Cwd string

	// ToolName is set for PreToolUse / PostToolUse events. Exposed
	// so the Phase-6 composer can surface "waiting on <tool>" hints.
	ToolName string

	// ReceivedAt is when ocman observed the event (handler wall-clock).
	// Drives staleness detection in the live-state cache.
	ReceivedAt time.Time

	// Ignored=true marks events the adapter chose not to act on:
	// missing session_id, unknown event_name, etc. The handler still
	// replies 200 — hooks should never fail the CLI command.
	Ignored bool
}

// hookEventNames is the closed set of event types the adapter maps to
// live-state transitions. Any event outside this set is parsed as
// Ignored so future CLI versions can't break the handler.
var hookEventNames = map[string]struct{}{
	"UserPromptSubmit": {},
	"SessionStart":     {},
	"PreToolUse":       {},
	"PostToolUse":      {},
	"Stop":             {},
	"SubagentStop":     {},
	"Notification":     {},
}

// rawHookPayload is the JSON envelope posted by the Claude Code CLI.
// Only the fields we act on are declared; everything else is dropped.
type rawHookPayload struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Cwd           string `json:"cwd"`
	ToolName      string `json:"tool_name"`
	// Reason, UserPrompt, Message are present on various event types
	// but don't affect live state; ignored deliberately.
}

// parseHookPayload decodes a raw hook payload into a hookEvent. The
// only hard error is invalid JSON — everything else degrades to
// Ignored=true rather than failing the CLI hook invocation.
func parseHookPayload(raw []byte, receivedAt time.Time) (hookEvent, error) {
	var p rawHookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return hookEvent{}, fmt.Errorf("decode hook payload: %w", err)
	}
	ev := hookEvent{
		EventName:  p.HookEventName,
		SessionID:  p.SessionID,
		Cwd:        p.Cwd,
		ToolName:   p.ToolName,
		ReceivedAt: receivedAt,
	}
	if p.SessionID == "" {
		ev.Ignored = true
		return ev, nil
	}
	if _, ok := hookEventNames[p.HookEventName]; !ok {
		ev.Ignored = true
	}
	return ev, nil
}

// liveStateDelta describes how a hookEvent mutates whatever live state
// the cache already holds for a session. Separate from LiveState so
// we can express "do not touch this field" (a zero Status means "no
// status change" rather than "set status to empty string").
//
// The cache applies deltas in order of arrival — later events win
// for the same field. The only field with a distinct clear signal is
// PendingPermission, because a Stop event should both set status=done
// and clear any stuck permission flag from a prior Notification.
type liveStateDelta struct {
	// Status is the new status ("busy", "waiting", "done", "error"),
	// or "" to leave the field unchanged.
	Status string

	// PendingPermission = true asserts a permission prompt is open.
	PendingPermission bool

	// ClearPendingPermission = true tells the cache to forcibly
	// clear any existing PendingPermission flag. Distinct from
	// PendingPermission=false so the zero-delta is truly inert.
	ClearPendingPermission bool
}

// toLiveStateDelta maps a hookEvent into the cache mutation it should
// produce. See spec/multi-agent-support/architecture.md §Phase 5 for
// the rationale behind each mapping.
func (ev hookEvent) toLiveStateDelta() liveStateDelta {
	switch ev.EventName {
	case "UserPromptSubmit":
		// A fresh user prompt means Claude is now busy; any prior
		// permission prompt was resolved (either granted or denied)
		// before the next prompt could be submitted.
		return liveStateDelta{Status: "busy", ClearPendingPermission: true}
	case "SessionStart":
		// SessionStart fires before the first message of every session.
		// The TUI is up and about to await input; "busy" is the closest
		// match — we don't have a distinct "initialising" state.
		return liveStateDelta{Status: "busy"}
	case "PreToolUse", "PostToolUse":
		// Claude is actively doing work. PostToolUse is not a terminal
		// signal — another tool or Stop will follow.
		return liveStateDelta{Status: "busy"}
	case "Stop":
		// End of an assistant turn. Treat as "done" rather than
		// "waiting" because the ambient assumption for a ready TUI
		// is that the user is about to type the next prompt.
		// Also clears any stuck permission flag.
		return liveStateDelta{Status: "done", ClearPendingPermission: true}
	case "SubagentStop":
		// A subagent finished; the main session may still be busy.
		// For now we collapse to "done" — the next PreToolUse /
		// Stop from the parent session will update again.
		return liveStateDelta{Status: "done"}
	case "Notification":
		// Notifications currently cover permission asks; flag the
		// session as pending. If Claude adds more notification types
		// we'll refine this.
		return liveStateDelta{PendingPermission: true}
	}
	// Unknown / ignored events produce an inert delta.
	return liveStateDelta{}
}
