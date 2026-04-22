package claudecode

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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

	// ToolInput is the raw JSON of the tool's input object for
	// PreToolUse / PostToolUse events. Used to derive a one-line
	// summary (target path for Read, command for Bash, ...) that
	// the frontend renders under the live Task card. Only kept while
	// a tool is active — dropped once we've built a Summary.
	ToolInput json.RawMessage

	// TranscriptPath is the absolute path to the parent session's jsonl.
	// Claude Code's hook docs are clear that this field always points at
	// the top-level transcript, even for events fired from inside a
	// sub-agent — the sub-agent's own jsonl is surfaced via the separate
	// AgentTranscriptPath field (SubagentStop only). We keep this field
	// for diagnostics and as a last-resort fallback in subagentID().
	TranscriptPath string

	// AgentID / AgentType are set on every hook event fired from within
	// a sub-agent (PreToolUse / PostToolUse / SubagentStop …). AgentID
	// is the authoritative sub-agent identifier and is what subagentID()
	// returns when available. Empty when the event is parent-level work.
	AgentID   string
	AgentType string

	// AgentTranscriptPath is the absolute path to the sub-agent's jsonl.
	// Documented on SubagentStop; Claude Code may also set it on other
	// sub-agent-scoped events. Used as a fallback to derive the sub-agent
	// id when AgentID is absent.
	AgentTranscriptPath string

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
	SessionID           string          `json:"session_id"`
	HookEventName       string          `json:"hook_event_name"`
	Cwd                 string          `json:"cwd"`
	ToolName            string          `json:"tool_name"`
	ToolInput           json.RawMessage `json:"tool_input"`
	TranscriptPath      string          `json:"transcript_path"`
	AgentID             string          `json:"agent_id"`              // set inside a sub-agent
	AgentType           string          `json:"agent_type"`            // ditto
	AgentTranscriptPath string          `json:"agent_transcript_path"` // SubagentStop (and sub-agent-scoped events)
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
		EventName:           p.HookEventName,
		SessionID:           p.SessionID,
		Cwd:                 p.Cwd,
		ToolName:            p.ToolName,
		ToolInput:           p.ToolInput,
		TranscriptPath:      p.TranscriptPath,
		AgentID:             p.AgentID,
		AgentType:           p.AgentType,
		AgentTranscriptPath: p.AgentTranscriptPath,
		ReceivedAt:          receivedAt,
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

	// ToolStart records that a new tool invocation began. The cache
	// pushes this onto the per-session active-tool list keyed by
	// (SubagentID, ToolName). SubagentID="" means the tool is
	// running on the parent session rather than a sub-agent.
	ToolStart *toolActivity

	// ToolEnd records that an invocation completed (PostToolUse).
	// The cache removes the matching entry from the active-tool list.
	ToolEnd *toolActivity

	// SubagentEnd signals SubagentStop — clear every active tool for
	// the given SubagentID (since that sub-agent is done).
	SubagentEnd string
}

// toolActivity pairs a tool invocation with the sub-agent it belongs
// to (empty SubagentID = parent session).
type toolActivity struct {
	SubagentID string
	ToolName   string
	Summary    string
}

// subagentID returns the authoritative sub-agent identifier for the
// event, preferring the explicit AgentID field (set by Claude Code on
// every sub-agent-scoped event) over a path-derived fallback. Returns
// "" for parent-level work, which the cache treats as "tool belongs to
// the parent session, not a sub-agent".
//
// Preference order:
//  1. ev.AgentID — authoritative when present (docs guarantee it for
//     every hook fired from inside a sub-agent).
//  2. deriveSubagentID(ev.AgentTranscriptPath) — documented on
//     SubagentStop; also a useful fallback if a future CLI version
//     sets it on PreToolUse/PostToolUse too.
//  3. deriveSubagentID(ev.TranscriptPath) — historical fallback kept
//     only because it's harmless (transcript_path is the PARENT jsonl
//     for sub-agent events, so this typically returns "" anyway) and
//     keeps pre-existing test fixtures working.
func (ev hookEvent) subagentID() string {
	if ev.AgentID != "" {
		return ev.AgentID
	}
	if id := deriveSubagentID(ev.AgentTranscriptPath); id != "" {
		return id
	}
	return deriveSubagentID(ev.TranscriptPath)
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
	case "PreToolUse":
		// Claude is actively doing work. Track the start of this tool
		// call so the UI can render a live list under the Task card.
		return liveStateDelta{
			Status: "busy",
			ToolStart: &toolActivity{
				SubagentID: ev.subagentID(),
				ToolName:   ev.ToolName,
				Summary:    summariseToolInput(ev.ToolName, ev.ToolInput),
			},
		}
	case "PostToolUse":
		// Tool finished; clear its entry but stay busy (another tool
		// or Stop will follow).
		return liveStateDelta{
			Status: "busy",
			ToolEnd: &toolActivity{
				SubagentID: ev.subagentID(),
				ToolName:   ev.ToolName,
			},
		}
	case "Stop":
		// End of an assistant turn. Treat as "done" rather than
		// "waiting" because the ambient assumption for a ready TUI
		// is that the user is about to type the next prompt.
		// Also clears any stuck permission flag.
		return liveStateDelta{Status: "done", ClearPendingPermission: true}
	case "SubagentStop":
		// A sub-agent finished; clear all of ITS active tool entries.
		// The parent session's overall status is unchanged — another
		// PreToolUse / Stop from the parent will update again. We
		// deliberately do NOT flip Status to "done" here (the parent
		// may still be working); the 2 min busy TTL guards against
		// a dropped parent Stop.
		//
		// CRITICAL: if we can't identify the sub-agent, emit an inert
		// delta. Without this guard, SubagentEnd="" would fall through
		// to the cache's "clear every entry with matching SubagentID"
		// loop and wipe the parent's own Task entry (Task tool_use has
		// SubagentID="" because it runs at the parent level). Losing
		// the Task entry means the UI stops showing live progress.
		id := ev.subagentID()
		if id == "" {
			return liveStateDelta{}
		}
		return liveStateDelta{SubagentEnd: id}
	case "Notification":
		// Notifications currently cover permission asks; flag the
		// session as pending. If Claude adds more notification types
		// we'll refine this.
		return liveStateDelta{PendingPermission: true}
	}
	// Unknown / ignored events produce an inert delta.
	return liveStateDelta{}
}

// deriveSubagentID returns the sub-agent identifier encoded in a
// Claude Code transcript_path, or "" if the path refers to a top-level
// session transcript.
//
// Sub-agent transcripts live at
//
//	~/.claude/projects/<dir>/<parentUUID>/subagents/agent-<agentId>.jsonl
//
// — everything else is parent-session data and yields "".
func deriveSubagentID(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	dir, file := filepath.Split(transcriptPath)
	if !strings.HasSuffix(filepath.Clean(dir), string(filepath.Separator)+"subagents") {
		return ""
	}
	name := strings.TrimSuffix(file, ".jsonl")
	name = strings.TrimPrefix(name, "agent-")
	return name
}

// summariseToolInput returns a short, display-friendly summary of a
// tool's input object. Best-effort: unknown tools, missing keys, and
// malformed JSON all yield "" so the renderer falls back to the tool
// name alone.
//
// Supported (heuristic) shapes match Claude Code's built-in tools:
//   - Read/Write/Edit: file_path
//   - Bash: command (truncated)
//   - Grep/Glob: pattern (+ path)
//   - WebFetch: url
func summariseToolInput(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var probe struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Pattern  string `json:"pattern"`
		Command  string `json:"command"`
		URL      string `json:"url"`
		Query    string `json:"query"`
	}
	if err := json.Unmarshal(input, &probe); err != nil {
		return ""
	}
	const maxLen = 120
	pick := func(first string, rest ...string) string {
		if first != "" {
			return first
		}
		for _, s := range rest {
			if s != "" {
				return s
			}
		}
		return ""
	}
	var s string
	switch toolName {
	case "Read", "Write", "Edit", "NotebookEdit":
		s = probe.FilePath
	case "Bash", "BashOutput":
		s = probe.Command
	case "Grep":
		s = probe.Pattern
		if probe.Path != "" {
			s += " @ " + probe.Path
		}
	case "Glob":
		s = pick(probe.Pattern, probe.Path)
	case "WebFetch", "WebSearch":
		s = pick(probe.URL, probe.Query)
	default:
		s = pick(probe.FilePath, probe.Path, probe.Pattern, probe.Command, probe.URL, probe.Query)
	}
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}
