package db

import (
	"encoding/json"
)

// InferSessionStatus determines the session status from the last message's attributes.
//   - "error"   = last assistant message has an error or finish == "error"
//   - "waiting" = last assistant message has a finish reason (turn complete)
//   - "busy"    = last message is assistant with no finish reason (still streaming)
//   - "done"    = no messages, last message is from the user, or last message
//                 is a synthesized non-LLM assistant message that has already
//                 reached its terminal state (e.g. the assistant envelope
//                 produced by POST /session/{id}/shell, which holds a single
//                 completed bash tool part and never receives a `finish`
//                 because no LLM turn ran).
//
// synthesizedTerminal is true when the last message is an assistant message
// whose parts indicate a non-LLM origin that has already finished:
//   - the message has at least one part,
//   - none of the parts is a `step-start` (no LLM turn was initiated), and
//   - none of the parts is in a `running` state (no tool still in flight).
//
// When synthesizedTerminal is true and the message has no `finish`/`error`,
// the session is reported as "done" instead of the misleading "busy". This
// stops the busy spinner — and the cascading "queued" badge on subsequent
// user messages — from sticking forever after a `!`-prefixed shell command.
func InferSessionStatus(lastRole, lastFinish, lastError string, synthesizedTerminal bool) string {
	if lastRole == "assistant" {
		if lastFinish == "error" || lastError != "" {
			return "error"
		}
		if lastFinish != "" {
			return "waiting"
		}
		if synthesizedTerminal {
			return "done"
		}
		return "busy"
	}
	return "done"
}

// Session represents a coding-platform session (OpenCode, Claude Code, ...).
//
// Fields that have no equivalent in a given platform are zero-valued or nil;
// see FR-14 in spec/multi-agent-support/requirements.md. The Platform field
// identifies the owning adapter and is populated by every adapter.
//
// Terminology: Platform here is the coding-agent tool producing the session
// (OpenCode / Claude Code / ...). Don't confuse it with the composer-level
// "agent" role exposed by some platforms (MessageData.Agent below, OpenCode's
// /agent catalog) — that's a narrower concept within a single session.
type Session struct {
	ID                string  `json:"id"`
	Platform          string  `json:"platform"` // owning adapter ID, e.g. "opencode", "claude-code"
	ProjectID         string  `json:"projectId"`
	// ParentID is the session this one descends from, when any. Two
	// independent sources can populate it: OpenCode's own
	// `session.parent_id` (subagent sessions), and ocman's
	// state.db `child_sessions.parent_session_id` (sessions spawned
	// via the MCP split tools). Empty for top-level sessions. The
	// frontend uses it to render the list as a parent/child tree.
	ParentID          string  `json:"parentId,omitempty"`
	Title             string  `json:"title"`
	Directory         string  `json:"directory"`
	TimeCreated       int64   `json:"timeCreated"`
	TimeUpdated       int64   `json:"timeUpdated"`
	SummaryAdditions  *int    `json:"summaryAdditions"`
	SummaryDeletions  *int    `json:"summaryDeletions"`
	SummaryFiles      *int    `json:"summaryFiles"`
	ShareURL          *string `json:"shareUrl"`
	MessageCount      int     `json:"messageCount"`
	DurationMs        int64   `json:"durationMs"`
	// ActiveDurationMs is the time the agent was actually working on a
	// turn, computed as the sum of (time.completed - time.created)
	// across assistant messages. Excludes idle gaps between turns
	// (user think time, permission prompts answered between turns).
	// Zero when the platform doesn't expose per-turn timestamps.
	ActiveDurationMs  int64   `json:"activeDurationMs"`
	TotalInputTokens  int64   `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalCost         float64 `json:"totalCost"`
	Status            string  `json:"status"` // "waiting", "busy", "done", or "error"
	// LiveConnection is true when the adapter has a live channel to this
	// session's running agent process. For OpenCode this means a --port
	// was discovered for the session's cwd; for Claude Code it means the
	// jsonl is currently held open or a hook event was observed recently.
	LiveConnection    bool `json:"liveConnection"`
	PendingPermission bool `json:"pendingPermission"` // agent has a pending permission request for this session
	PendingQuestion   bool `json:"pendingQuestion"`   // agent has a pending question for this session
	Archived bool  `json:"archived"`
	Seen     bool  `json:"seen"`
	Pinned   bool  `json:"pinned"`
	PinnedAt int64 `json:"pinnedAt"`
	// SeenTimeUpdated is the session's time_updated at the moment the
	// user last viewed it (0 when never seen). Used by the frontend to
	// compute a "first unread" marker and a per-session unread badge
	// without an extra round-trip. Populated by applySessionState.
	SeenTimeUpdated int64 `json:"seenTimeUpdated"`
	// UnreadCount is the number of messages in this session whose
	// time_created is strictly greater than SeenTimeUpdated. Zero when
	// the session is fully seen, when the platform doesn't expose
	// per-message timestamps, or when the user has never opened it
	// (in which case the frontend treats the whole session as new).
	// Populated by applySessionState via the platform's optional
	// UnreadCounter interface.
	UnreadCount int `json:"unreadCount"`
	// Notice carries a normalized, platform-agnostic explanation of a
	// transient session condition (e.g. rate-limit backoff). Populated
	// by the server's applySessionNotice step; nil when no notice
	// applies. Omitted from JSON when nil.
	Notice *SessionNotice `json:"notice,omitempty"`
	// RemoteID / RemoteName are display-only host attributes stamped by
	// the owning adapter for multi-remote support (AD-7). RemoteID is
	// "local" for the hub's own machine, else the remote's random ID;
	// RemoteName is the host display label / hostname ("This machine"
	// for local). The frontend renders RemoteName as a host badge; no
	// behaviour keys off these values.
	RemoteID   string `json:"remoteId,omitempty"`
	RemoteName string `json:"remoteName,omitempty"`
	// Stale marks last-known data served from an offline remote (AD-13).
	// The frontend renders stale rows dimmed with an offline indicator.
	Stale bool `json:"stale,omitempty"`
	// Note: GitInfo used to live here, populated by /api/sessions on
	// the request path via a synchronous fan-out of `git status` per
	// directory. It now lives in the gitinfo package (gitinfo.Info)
	// and is served by /api/git/info, fetched on demand by the
	// frontend components that need it. See docs/profiling.md.

	// Internal-only fields used by the notice normalizer. Populated
	// by the DB query / adapter but never serialized to JSON.
	LastErrorName    string `json:"-"`
	LastErrorMessage string `json:"-"`
	LastErrorAt      int64  `json:"-"`
}

// SessionNotice carries a normalized, platform-agnostic explanation of a
// transient session condition. Kinds include "rate_limit" and
// "provider_overloaded", surfaced when the latest assistant error matches a
// known transient provider pattern. The frontend renders this as a banner /
// tooltip without inspecting the platform field.
type SessionNotice struct {
	Kind    string `json:"kind"`    // e.g. "rate_limit" or "provider_overloaded"
	Message string `json:"message"` // user-facing summary
	RetryAt int64  `json:"retryAt"` // unix ms when retry is expected, 0 when unknown
	Attempt int    `json:"attempt"` // retry attempt number, 0 when unknown
}

// SessionArchiveCandidate carries the minimal session data needed for archive jobs.
type SessionArchiveCandidate struct {
	ID          string
	TimeUpdated int64
}

// Message represents an OpenCode message.
type Message struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"sessionId"`
	TimeCreated int64           `json:"timeCreated"`
	Data        json.RawMessage `json:"data"`
}

// MessageData is the parsed data from a message.
//
// Agent here is the composer-level agent role used within a single
// OpenCode session (e.g. "build", "plan", a subagent name). This is a
// different concept from Session.Platform, which identifies the coding
// tool that produced the session.
type MessageData struct {
	Role       string     `json:"role"`
	Agent      string     `json:"agent"` // composer-agent role (OpenCode: "build", "plan", subagent name)
	Mode       string     `json:"mode"`
	ModelID    string     `json:"modelID"`
	ProviderID string     `json:"providerID"`
	Cost       float64    `json:"cost"`
	Tokens     *TokenInfo `json:"tokens"`
	Time       *TimeInfo  `json:"time"`
	Model      *ModelRef  `json:"model"`
	Finish     string     `json:"finish"`
}

// TokenInfo holds token usage details for a message.
type TokenInfo struct {
	Input     int64      `json:"input"`
	Output    int64      `json:"output"`
	Reasoning int64      `json:"reasoning"`
	Cache     *CacheInfo `json:"cache"`
}

// CacheInfo holds cache read/write counts.
type CacheInfo struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}

// TimeInfo holds created/completed timestamps.
type TimeInfo struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

// ModelRef holds a provider/model reference.
type ModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// Part represents a message part.
type Part struct {
	ID          string          `json:"id"`
	MessageID   string          `json:"messageId"`
	SessionID   string          `json:"sessionId"`
	TimeCreated int64           `json:"timeCreated"`
	Data        json.RawMessage `json:"data"`
}

// Stats holds aggregate statistics.
type Stats struct {
	TotalSessions  int     `json:"totalSessions"`
	TotalMessages  int     `json:"totalMessages"`
	TotalProjects  int     `json:"totalProjects"`
	TotalTokensIn  int64   `json:"totalTokensIn"`
	TotalTokensOut int64   `json:"totalTokensOut"`
	TotalCost      float64 `json:"totalCost"`
}

// MetricsSummary holds the dashboard KPI cards for request analytics.
type MetricsSummary struct {
	Requests         int     `json:"requests"`
	TotalTokens      int64   `json:"totalTokens"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	AvgTokensPerSec  float64 `json:"avgTokensPerSec"`
	AvgDurationMs    float64 `json:"avgDurationMs"`
	TotalDurationMs  int64   `json:"totalDurationMs"`
	CacheHitRate     float64 `json:"cacheHitRate"`
	TotalCost        float64 `json:"totalCost"`
	TotalCalcCost    float64 `json:"totalCalcCost"`
	// TotalEffectiveCost is the headline cost: per request it uses the
	// platform-reported cost when that is non-zero, otherwise the
	// token-derived estimate. This reconciles subscription-plan
	// sessions (reported $0) with API-priced sessions so the summary
	// matches what the per-row tables show.
	TotalEffectiveCost float64 `json:"totalEffectiveCost"`
}

// MetricsPoint holds chart data for a time bucket (hour or day).
type MetricsPoint struct {
	// Label is the human-readable bucket label ("2026-04-16 14" or "2026-04-16").
	Label               string  `json:"label"`
	AvgOutputTokensSec      float64 `json:"avgOutputTokensSec"`
	CumulativeCost          float64 `json:"cumulativeCost"`
	CumulativeCalcCost      float64 `json:"cumulativeCalcCost"`
	CumulativeEffectiveCost float64 `json:"cumulativeEffectiveCost"`
	InputTokens         int64   `json:"inputTokens"`
	CacheReadTokens     int64   `json:"cacheReadTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	AvgDurationMs       float64 `json:"avgDurationMs"`
	AvgCacheEfficiency  float64 `json:"avgCacheEfficiency"`
	Count               int     `json:"count"`
}

// StopReasonCount holds the count for a stop reason.
type StopReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// RequestLogEntry holds request-level metrics for the request log table.
type RequestLogEntry struct {
	ID               string  `json:"id"`
	SessionID        string  `json:"sessionId"`
	TimeCreated      int64   `json:"timeCreated"`
	Agent            string  `json:"agent"`
	Model            string  `json:"model"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TokensPerSecond  float64 `json:"tokensPerSecond"`
	DurationMs       int64   `json:"durationMs"`
	Cost             float64 `json:"cost"`
	CalcCost         float64 `json:"calcCost"`
	// EffectiveCost is Cost when reported (>0), otherwise CalcCost.
	EffectiveCost float64 `json:"effectiveCost"`
	StopReason    string  `json:"stopReason"`
}

// SessionLogEntry holds per-session aggregated metrics for the session log table.
// Values are derived by aggregating the assistant requests that fall within the
// currently-applied agent/model/time filters, so it reflects the same scope as
// the other metrics panels on the dashboard.
type SessionLogEntry struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Directory        string   `json:"directory"`
	FirstRequestTime int64    `json:"firstRequestTime"`
	LastRequestTime  int64    `json:"lastRequestTime"`
	Requests         int      `json:"requests"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	TotalDurationMs  int64    `json:"totalDurationMs"`
	AvgTokensPerSec  float64  `json:"avgTokensPerSec"`
	Cost             float64  `json:"cost"`
	CalcCost         float64  `json:"calcCost"`
	// EffectiveCost sums each request's effective cost (reported when
	// >0, else estimate) so it reconciles with the dashboard summary.
	EffectiveCost float64  `json:"effectiveCost"`
	Agents        []string `json:"agents"`
	Models        []string `json:"models"`
	ErrorCount    int      `json:"errorCount"`
}

// ProjectLogEntry holds per-project (directory) aggregated metrics.
type ProjectLogEntry struct {
	Directory        string   `json:"directory"`
	Sessions         int      `json:"sessions"`
	Requests         int      `json:"requests"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheWriteTokens int64    `json:"cacheWriteTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	TotalDurationMs  int64    `json:"totalDurationMs"`
	AvgTokensPerSec  float64  `json:"avgTokensPerSec"`
	Cost             float64  `json:"cost"`
	CalcCost         float64  `json:"calcCost"`
	// EffectiveCost sums each request's effective cost (reported when
	// >0, else estimate) so it reconciles with the dashboard summary.
	EffectiveCost   float64  `json:"effectiveCost"`
	Models          []string `json:"models"`
	ErrorCount      int      `json:"errorCount"`
	LastRequestTime int64    `json:"lastRequestTime"`
}

// MetricsDashboard holds the full metrics dashboard payload.
type MetricsDashboard struct {
	AvailableAgents []string           `json:"availableAgents"`
	AvailableModels []string           `json:"availableModels"`
	Summary         MetricsSummary     `json:"summary"`
	Series          []MetricsPoint     `json:"series"`
	CostByModel     MetricsCostByModel `json:"costByModel"`
	StopReasons     []StopReasonCount  `json:"stopReasons"`
	Requests        []RequestLogEntry  `json:"requests"`
	TotalRequests   int                `json:"totalRequests"`
	Sessions        []SessionLogEntry  `json:"sessions"`
	TotalSessions   int                `json:"totalSessions"`
	Projects        []ProjectLogEntry  `json:"projects"`
	TotalProjects   int                `json:"totalProjects"`
}

// MetricsCostByModel holds the cumulative cost series broken down by
// model. Models is the ordered list of series keys (highest-total cost
// first; an "Other" bucket trails when there are more than
// CostByModelTopN distinct models). Series has the same length as
// MetricsDashboard.Series and each ModelCostPoint.Costs is parallel to
// Models. Values are cumulative platform-reported cost in USD.
type MetricsCostByModel struct {
	Models []string         `json:"models"`
	Series []ModelCostPoint `json:"series"`
}

// ModelCostPoint is one bucket of the per-model cumulative cost series.
type ModelCostPoint struct {
	Label string    `json:"label"`
	Costs []float64 `json:"costs"`
}

// ProjectStats holds per-directory aggregated data.
type ProjectStats struct {
	Directory      string  `json:"directory"`
	SessionCount   int     `json:"sessionCount"`
	MessageCount   int     `json:"messageCount"`
	LastUsed       int64   `json:"lastUsed"`
	TotalTokensIn  int64   `json:"totalTokensIn"`
	TotalTokensOut int64   `json:"totalTokensOut"`
	TotalCost      float64 `json:"totalCost"`
	// Archived is set by the server layer (not the DB query) from
	// ocman's own state.db: true when the project's folded root is
	// archived and no session is newer than the archive time.
	Archived bool `json:"archived,omitempty"`
	// RemoteID / RemoteName / Platform are set by the server layer for
	// projects that live on a connected remote (empty for local ones).
	// RemoteID is the remote instance ID; Platform is the compound
	// platform id (r-<remoteID>:<base>) used when creating a session.
	RemoteID   string `json:"remoteId,omitempty"`
	RemoteName string `json:"remoteName,omitempty"`
	Platform   string `json:"platform,omitempty"`
}

// DailyActivity holds activity counts per day.
type DailyActivity struct {
	Date         string `json:"date"`
	Sessions     int    `json:"sessions"`
	Messages     int    `json:"messages"`
	UserMessages int    `json:"userMessages"`
}

// ModelUsage holds per-model usage info.
type ModelUsage struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Count      int    `json:"count"`
	TokensIn   int64  `json:"tokensIn"`
	TokensOut  int64  `json:"tokensOut"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
}

// SessionDefaults holds the fallback composer settings for a session.
type SessionDefaults struct {
	Agent string `json:"agent"`
	Model string `json:"model"`
}

// HourlyActivity holds activity counts per hour of day.
type HourlyActivity struct {
	Hour     int `json:"hour"`
	Sessions int `json:"sessions"`
}

// LLMMessageRow is a lightweight projection of an assistant message used by
// the LLM metrics scanner. It contains only the fields needed to emit OTel
// counters and histograms — no raw JSON, no session metadata.
type LLMMessageRow struct {
	TimeCreated      int64
	SessionID        string  // owning session, for per-session metric scoping
	Model            string  // "provider/model"
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	Cost             float64
	StopReason       string
	DurationMs       int64 // time.completed - time.created; 0 if unavailable
}

// HourlyTokensByModel holds token counts for a specific calendar hour and model.
type HourlyTokensByModel struct {
	Datetime  string `json:"datetime"` // "YYYY-MM-DD HH" in local time
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	TokensIn  int64  `json:"tokensIn"`
	TokensOut int64  `json:"tokensOut"`
}
