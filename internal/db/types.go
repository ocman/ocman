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
	// Note: GitInfo used to live here, populated by /api/sessions on
	// the request path via a synchronous fan-out of `git status` per
	// directory. It now lives in the gitinfo package (gitinfo.Info)
	// and is served by /api/git/info, fetched on demand by the
	// frontend components that need it. See docs/profiling.md.
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
}

// MetricsPoint holds chart data for a time bucket (hour or day).
type MetricsPoint struct {
	// Label is the human-readable bucket label ("2026-04-16 14" or "2026-04-16").
	Label               string  `json:"label"`
	AvgOutputTokensSec  float64 `json:"avgOutputTokensSec"`
	CumulativeCost      float64 `json:"cumulativeCost"`
	CumulativeCalcCost  float64 `json:"cumulativeCalcCost"`
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
	StopReason       string  `json:"stopReason"`
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
	Agents           []string `json:"agents"`
	Models           []string `json:"models"`
	ErrorCount       int      `json:"errorCount"`
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
	Models           []string `json:"models"`
	ErrorCount       int      `json:"errorCount"`
	LastRequestTime  int64    `json:"lastRequestTime"`
}

// MetricsDashboard holds the full metrics dashboard payload.
type MetricsDashboard struct {
	AvailableAgents []string          `json:"availableAgents"`
	AvailableModels []string          `json:"availableModels"`
	Summary         MetricsSummary    `json:"summary"`
	Series          []MetricsPoint    `json:"series"`
	StopReasons     []StopReasonCount `json:"stopReasons"`
	Requests        []RequestLogEntry `json:"requests"`
	TotalRequests   int               `json:"totalRequests"`
	Sessions        []SessionLogEntry `json:"sessions"`
	TotalSessions   int               `json:"totalSessions"`
	Projects        []ProjectLogEntry `json:"projects"`
	TotalProjects   int               `json:"totalProjects"`
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
}

// DailyActivity holds activity counts per day.
type DailyActivity struct {
	Date     string `json:"date"`
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
}

// ModelUsage holds per-model usage info.
type ModelUsage struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Count     int    `json:"count"`
	TokensIn  int64  `json:"tokensIn"`
	TokensOut int64  `json:"tokensOut"`
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

// HourlyTokensByModel holds token counts for a specific calendar hour and model.
type HourlyTokensByModel struct {
	Datetime  string `json:"datetime"` // "YYYY-MM-DD HH" in local time
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	TokensIn  int64  `json:"tokensIn"`
	TokensOut int64  `json:"tokensOut"`
}
