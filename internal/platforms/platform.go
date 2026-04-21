// Package platforms defines the multi-platform adapter contract.
//
// Terminology. Ocman distinguishes two different "agent" concepts:
//
//   - Platform: the coding-agent tool that produces a session — OpenCode,
//     Claude Code, Codex, Gemini, ... This package is about platforms.
//     Every Session in ocman is owned by exactly one platform.
//   - Agent: the narrower, composer-level concept surfaced by some
//     platforms (notably OpenCode's /agent catalog: "build", "plan",
//     subagents, ...). That name is preserved in MessageData.Agent, the
//     OpenCode composer-agent catalog, and the frontend AgentPicker — it
//     describes a role within a single session, not the platform.
//
// Every platform adapter implements Platform. HTTP handlers resolve an
// adapter for each request (from ?platform= or a reverse lookup) and
// delegate; no platform-specific code lives in the server package.
//
// See spec/multi-agent-support/architecture.md for the design rationale.
package platforms

import (
	"context"
	"io"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// ID identifies a platform adapter, e.g. "opencode", "claude-code".
type ID string

// SessionDetail is the full response shape for fetching one session:
// session metadata, paginated messages and their parts, and composer
// defaults / context token usage.
//
// Adapters populate only the fields they support; unsupported fields
// stay at their zero value and are surfaced as null/0 on the wire per
// FR-14. Every adapter must return the same shape — handlers trust the
// typed fields rather than extracting from a map.
type SessionDetail struct {
	Session           *db.Session  `json:"session"`
	Messages          []db.Message `json:"messages"`
	Parts             []db.Part    `json:"parts"`
	TotalMessages     int          `json:"totalMessages"`
	ContextTokenCount int64        `json:"contextTokenCount,omitempty"`
	DefaultAgent      string       `json:"defaultAgent,omitempty"` // composer-agent (OpenCode "build"/"plan"/...) default
	DefaultModel      string       `json:"defaultModel,omitempty"`
}

// Capabilities describes what a platform supports. The frontend uses
// these flags (exposed via GET /api/capabilities) to gate UI without
// branching on platform identity.
//
// "AgentCatalog" here refers to a platform's composer-agent catalog —
// OpenCode's /agent endpoint with its "build"/"plan"/subagent roles.
// Platforms with no such concept (Claude Code) report false.
type Capabilities struct {
	Composer          bool `json:"composer"`
	RespondPermission bool `json:"respondPermission"`
	RespondQuestion   bool `json:"respondQuestion"`
	Abort             bool `json:"abort"`
	Compact           bool `json:"compact"`
	Events            bool `json:"events"`       // SSE stream of live session events
	AgentCatalog      bool `json:"agentCatalog"` // adapter exposes a composer-agent catalog
	ModelCatalog      bool `json:"modelCatalog"` // adapter exposes a per-session model catalog
	SlashCommands     bool `json:"slashCommands"`
	// LiveConnectionHint is a short, user-facing message explaining how
	// to establish the live connection to a running agent instance when
	// it's missing. Shown by the frontend next to disabled composers.
	// Empty when the platform has no such setup step (e.g. Claude Code,
	// whose live connection is based on CLI availability rather than a
	// discoverable port).
	LiveConnectionHint string `json:"liveConnectionHint,omitempty"`
}

// LiveState captures in-memory live status for a session, updated by
// out-of-band events (e.g. Claude Code hooks). Adapters that don't track
// live state return nil from LiveStatus.
type LiveState struct {
	Status            string    `json:"status"` // "busy", "waiting", "done", "error"
	PendingPermission bool      `json:"pendingPermission"`
	PendingQuestion   bool      `json:"pendingQuestion"`
	LastEventAt       time.Time `json:"lastEventAt"`
	// CurrentTools lists tool calls that are currently running on
	// behalf of this session's subagents (Claude Code Task tool_use
	// blocks). Populated from PreToolUse / PostToolUse / SubagentStop
	// hooks; empty when no live tool activity has been observed.
	// Consumed by the frontend to render a live list of tool calls
	// under a running subagent Task.
	CurrentTools []LiveTool `json:"currentTools,omitempty"`
}

// LiveTool describes one in-flight tool call observed via hook events.
// Scoped to a parent session, optionally keyed by SubagentID so the
// renderer can correlate it with the specific Task tool_use that
// spawned it. Adapters without a sub-agent concept leave SubagentID
// empty.
type LiveTool struct {
	// SubagentID correlates this tool call with the Task tool_use
	// that spawned the sub-agent. Derived by Claude Code from the
	// hook's transcript_path (agent-<id>.jsonl).
	SubagentID string `json:"subagentId,omitempty"`

	// ToolName is the raw tool identifier (e.g. "Read", "Grep").
	ToolName string `json:"toolName"`

	// Summary is an optional one-line description of what the tool
	// is doing — for Read it's the target path, for Bash the command,
	// etc. Best-effort; may be empty.
	Summary string `json:"summary,omitempty"`

	// StartedAt is when PreToolUse for this tool fired.
	StartedAt time.Time `json:"startedAt"`
}

// Platform is the contract every adapter must satisfy.
//
// Methods are called from HTTP handlers; implementations should be
// goroutine-safe and responsive to context cancellation.
//
// Capability-gated methods (those whose availability is reported by
// Capabilities()) should return ErrUnsupported when called on a
// platform that doesn't support them. Handlers will have already
// filtered these by consulting Capabilities(), but the adapter is
// still expected to fail safely if they don't.
type Platform interface {
	// ID returns the stable string identifier used in URLs and state.db.
	ID() ID

	// DisplayName returns a human-readable name for UI.
	DisplayName() string

	// Available reports whether this adapter has any usable data or
	// running instances. The registry uses this to skip adapters whose
	// backing stores don't exist on disk.
	Available(ctx context.Context) bool

	// Capabilities returns the capability flags for this platform.
	Capabilities() Capabilities

	// --- Read-only session data ---

	// Sessions lists all sessions for this platform, filtered by dir
	// (empty = all) and updated-after timestamp (0 = all).
	Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error)

	// Session returns full detail for a single session.
	Session(ctx context.Context, id string, limit, offset int) (*SessionDetail, error)

	// SessionsInactiveBefore returns archive candidates for the
	// background auto-archive job.
	SessionsInactiveBefore(ctx context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error)

	// LiveStatus returns in-memory live status for a session (nil if
	// none). Cheap: does not touch disk.
	LiveStatus(sessionID string) *LiveState

	// --- Session-scoped catalogs ---

	// AgentCatalog returns the platform's composer-agent catalog for
	// the given session (OpenCode: "build", "plan", subagents).
	// Platforms without the concept return nil, nil (empty slice).
	AgentCatalog(ctx context.Context, sessionID string) ([]AgentCatalogEntry, error)

	// SlashCommands returns available slash commands for the session.
	SlashCommands(ctx context.Context, sessionID string) ([]SlashCommandEntry, error)

	// SessionModels returns the session's model picker list.
	SessionModels(ctx context.Context, sessionID string) (*SessionModelsResponse, error)

	// ListPermissions returns pending permission prompts for the session.
	ListPermissions(ctx context.Context, sessionID string) ([]LivePrompt, error)

	// ListQuestions returns pending question prompts for the session.
	ListQuestions(ctx context.Context, sessionID string) ([]LivePrompt, error)

	// --- Mutating session operations ---

	// SendMessage submits a composer message to the session.
	SendMessage(ctx context.Context, req SendMessageRequest) error

	// ExecuteCommand runs a slash command.
	ExecuteCommand(ctx context.Context, req ExecuteCommandRequest) error

	// RespondPermission replies to a pending permission prompt.
	RespondPermission(ctx context.Context, req RespondPermissionRequest) error

	// RespondQuestion replies to a pending question prompt.
	RespondQuestion(ctx context.Context, req RespondQuestionRequest) error

	// RejectQuestion dismisses a pending question prompt.
	RejectQuestion(ctx context.Context, req RejectQuestionRequest) error

	// Abort cancels an in-flight response.
	Abort(ctx context.Context, req AbortRequest) error

	// RenameSession sets a new title for a session.
	RenameSession(ctx context.Context, req RenameSessionRequest) error

	// Compact compacts the session's history.
	Compact(ctx context.Context, req CompactRequest) error

	// CreateSession creates a new session and returns its ID. The
	// directory comes from the request; the adapter is free to apply
	// platform-specific defaults. CreateSession is called from the
	// platform-scoped /api/sessions endpoint rather than from a
	// session-scoped route.
	CreateSession(ctx context.Context, req CreateSessionRequest) (*CreateSessionResponse, error)

	// --- Streaming ---

	// ProxyEvents streams live session events to w (Server-Sent
	// Events format is the caller's responsibility). Blocks until
	// the upstream connection closes or ctx is cancelled.
	ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error
}

// Lifecycle is optional: adapters that need to run background work at
// server boot (install hooks, spawn watchers) implement Start/Stop.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
