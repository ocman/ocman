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
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// ID identifies a platform adapter, e.g. "opencode", "claude-code".
type ID string

// SessionDetail is the full response shape for fetching one session:
// session metadata, paginated messages and their parts, and any extras
// the adapter wants to surface (composer defaults, context token usage).
//
// Adapters populate only the fields they support; unsupported fields stay
// at their zero value and are surfaced as null/0 on the wire per FR-14.
type SessionDetail struct {
	Session           *db.Session            `json:"session"`
	Messages          []db.Message           `json:"messages"`
	Parts             []db.Part              `json:"parts"`
	TotalMessages     int                    `json:"totalMessages"`
	ContextTokenCount int64                  `json:"contextTokenCount,omitempty"`
	DefaultAgent      string                 `json:"defaultAgent,omitempty"` // composer-agent (OpenCode "build"/"plan"/...) default
	DefaultModel      string                 `json:"defaultModel,omitempty"`
	Extra             map[string]interface{} `json:"-"` // for adapter-specific fields rendered into the JSON envelope
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
}

// LiveState captures in-memory live status for a session, updated by
// out-of-band events (e.g. Claude Code hooks). Adapters that don't track
// live state return nil from LiveStatus.
type LiveState struct {
	Status            string    `json:"status"` // "busy", "waiting", "done", "error"
	PendingPermission bool      `json:"pendingPermission"`
	PendingQuestion   bool      `json:"pendingQuestion"`
	LastEventAt       time.Time `json:"lastEventAt"`
}

// Platform is the contract every adapter must satisfy.
//
// Methods are called from HTTP handlers; implementations should be
// goroutine-safe and responsive to context cancellation.
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
}

// Lifecycle is optional: adapters that need to run background work at
// server boot (install hooks, spawn watchers) implement Start/Stop.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
