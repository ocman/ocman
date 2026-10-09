package db

import (
	"encoding/json"
)

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
	Role       string          `json:"role"`
	Agent      string          `json:"agent"` // composer-agent role (OpenCode: "build", "plan", subagent name)
	Mode       string          `json:"mode"`
	ModelID    string          `json:"modelID"`
	ProviderID string          `json:"providerID"`
	Cost       float64         `json:"cost"`
	Tokens     *TokenInfo      `json:"tokens"`
	Time       *TimeInfo       `json:"time"`
	Model      *ModelRef       `json:"model"`
	Finish     string          `json:"finish"`
	Error      json.RawMessage `json:"error"`
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
	TotalSessions    int     `json:"totalSessions"`
	SubagentSessions int     `json:"subagentSessions"`
	TotalMessages    int     `json:"totalMessages"`
	TotalProjects    int     `json:"totalProjects"`
	TotalTokensIn    int64   `json:"totalTokensIn"`
	TotalTokensOut   int64   `json:"totalTokensOut"`
	TotalCost        float64 `json:"totalCost"`
	Version          string  `json:"version,omitempty"` // ocman build version; set by the handler
}

// MetricsSummary holds the dashboard KPI cards for request analytics.
type MetricsSummary struct {
	Requests            int        `json:"requests"`
	CompletedRequests   int        `json:"completedRequests"`
	SuccessfulRequests  int        `json:"successfulRequests"`
	ErrorRequests       int        `json:"errorRequests"`
	ErrorRate           float64    `json:"errorRate"`
	TotalTokens         int64      `json:"totalTokens"`
	InputTokens         int64      `json:"inputTokens"`
	OutputTokens        int64      `json:"outputTokens"`
	CacheReadTokens     int64      `json:"cacheReadTokens"`
	CacheWriteTokens    int64      `json:"cacheWriteTokens"`
	AvgTokensPerSec     float64    `json:"avgTokensPerSec"`
	AvgDurationMs       float64    `json:"avgDurationMs"`
	P50DurationMs       float64    `json:"p50DurationMs"`
	P95DurationMs       float64    `json:"p95DurationMs"`
	TotalDurationMs     int64      `json:"totalDurationMs"`
	CacheHitRate        float64    `json:"cacheHitRate"`
	TotalCost           float64    `json:"totalCost"`
	TotalCalcCost       float64    `json:"totalCalcCost"`
	EstimatedCostByType CostByType `json:"estimatedCostByType"`
	// TotalEffectiveCost is the headline cost: per request it uses the
	// platform-reported cost when that is non-zero, otherwise the
	// token-derived estimate. This reconciles subscription-plan
	// sessions (reported $0) with API-priced sessions so the summary
	// matches what the per-row tables show.
	TotalEffectiveCost       float64 `json:"totalEffectiveCost"`
	CostPerSuccessfulRequest float64 `json:"costPerSuccessfulRequest"`
}

type CostByType struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// MetricsPoint holds chart data for a time bucket (hour or day).
type MetricsPoint struct {
	// Label is the human-readable bucket label ("2026-04-16 14" or "2026-04-16").
	Label                   string  `json:"label"`
	AvgOutputTokensSec      float64 `json:"avgOutputTokensSec"`
	CumulativeCost          float64 `json:"cumulativeCost"`
	CumulativeCalcCost      float64 `json:"cumulativeCalcCost"`
	CumulativeEffectiveCost float64 `json:"cumulativeEffectiveCost"`
	InputTokens             int64   `json:"inputTokens"`
	CacheReadTokens         int64   `json:"cacheReadTokens"`
	OutputTokens            int64   `json:"outputTokens"`
	AvgDurationMs           float64 `json:"avgDurationMs"`
	P50DurationMs           float64 `json:"p50DurationMs"`
	P95DurationMs           float64 `json:"p95DurationMs"`
	AvgCacheEfficiency      float64 `json:"avgCacheEfficiency"`
	Count                   int     `json:"count"`
	CompletedRequests       int     `json:"completedRequests"`
	SuccessfulRequests      int     `json:"successfulRequests"`
	ErrorRequests           int     `json:"errorRequests"`
	ErrorRate               float64 `json:"errorRate"`
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
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Directory        string  `json:"directory"`
	FirstRequestTime int64   `json:"firstRequestTime"`
	LastRequestTime  int64   `json:"lastRequestTime"`
	Requests         int     `json:"requests"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	TotalDurationMs  int64   `json:"totalDurationMs"`
	AvgTokensPerSec  float64 `json:"avgTokensPerSec"`
	Cost             float64 `json:"cost"`
	CalcCost         float64 `json:"calcCost"`
	// EffectiveCost sums each request's effective cost (reported when
	// >0, else estimate) so it reconciles with the dashboard summary.
	EffectiveCost float64  `json:"effectiveCost"`
	Agents        []string `json:"agents"`
	Models        []string `json:"models"`
	ErrorCount    int      `json:"errorCount"`
}

// ProjectLogEntry holds per-project (directory) aggregated metrics.
type ProjectLogEntry struct {
	Directory        string  `json:"directory"`
	Sessions         int     `json:"sessions"`
	Requests         int     `json:"requests"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	TotalDurationMs  int64   `json:"totalDurationMs"`
	AvgTokensPerSec  float64 `json:"avgTokensPerSec"`
	Cost             float64 `json:"cost"`
	CalcCost         float64 `json:"calcCost"`
	// EffectiveCost sums each request's effective cost (reported when
	// >0, else estimate) so it reconciles with the dashboard summary.
	EffectiveCost   float64  `json:"effectiveCost"`
	Models          []string `json:"models"`
	ErrorCount      int      `json:"errorCount"`
	LastRequestTime int64    `json:"lastRequestTime"`
}

// MetricsDashboard holds the full metrics dashboard payload.
type MetricsDashboard struct {
	AvailableAgents []string       `json:"availableAgents"`
	AvailableModels []string       `json:"availableModels"`
	Summary         MetricsSummary `json:"summary"`
	Series          []MetricsPoint `json:"series"`
	// CostByModel is the cumulative effective cost aligned with Series.
	CostByModel MetricsCostByModel `json:"costByModel"`
	// DailyEstimatedCostByModel is token-price estimated cost grouped by day.
	DailyEstimatedCostByModel MetricsCostByModel `json:"dailyEstimatedCostByModel"`
	// DailyEffectiveCostByModel is non-cumulative effective cost grouped by day.
	DailyEffectiveCostByModel MetricsCostByModel `json:"dailyEffectiveCostByModel"`
	Agents                    []AgentMetrics     `json:"agents"`
	StopReasons               []StopReasonCount  `json:"stopReasons"`
	Requests                  []RequestLogEntry  `json:"requests"`
	TotalRequests             int                `json:"totalRequests"`
	Sessions                  []SessionLogEntry  `json:"sessions"`
	TotalSessions             int                `json:"totalSessions"`
	Projects                  []ProjectLogEntry  `json:"projects"`
	TotalProjects             int                `json:"totalProjects"`
}

// MetricsPerformance is the chart and summary portion of MetricsDashboard.
type MetricsPerformance struct {
	AvailableAgents           []string           `json:"availableAgents"`
	AvailableModels           []string           `json:"availableModels"`
	Summary                   MetricsSummary     `json:"summary"`
	Series                    []MetricsPoint     `json:"series"`
	CostByModel               MetricsCostByModel `json:"costByModel"`
	DailyEstimatedCostByModel MetricsCostByModel `json:"dailyEstimatedCostByModel"`
	DailyEffectiveCostByModel MetricsCostByModel `json:"dailyEffectiveCostByModel"`
	Agents                    []AgentMetrics     `json:"agents"`
	StopReasons               []StopReasonCount  `json:"stopReasons"`
}

type MetricsLogKind string

const (
	MetricsLogRequests MetricsLogKind = "request"
	MetricsLogSessions MetricsLogKind = "session"
	MetricsLogProjects MetricsLogKind = "project"
)

// MetricsLog contains one selected log grain. Unselected slices stay nil.
type MetricsLog struct {
	Kind            MetricsLogKind    `json:"kind"`
	AvailableAgents []string          `json:"availableAgents"`
	AvailableModels []string          `json:"availableModels"`
	Total           int               `json:"total"`
	Requests        []RequestLogEntry `json:"requests,omitempty"`
	Sessions        []SessionLogEntry `json:"sessions,omitempty"`
	Projects        []ProjectLogEntry `json:"projects,omitempty"`
}

// MetricsCostByModel holds a cost series broken down by
// model. Models is the ordered list of series keys (highest-total cost
// first; an "Other" bucket trails when there are more than
// CostByModelTopN distinct models). Each ModelCostPoint.Costs is parallel
// to Models; the containing MetricsDashboard field defines the granularity
// and cost semantics.
type MetricsCostByModel struct {
	Models []string         `json:"models"`
	Series []ModelCostPoint `json:"series"`
}

// ModelCostPoint is one bucket of the per-model cost series.
type ModelCostPoint struct {
	Label string    `json:"label"`
	Costs []float64 `json:"costs"`
}

// ProjectStats holds per-directory aggregated data.
type ProjectStats struct {
	ProjectKey     string   `json:"projectKey,omitempty"`
	UpstreamKeys   []string `json:"upstreamKeys,omitempty"`
	UpstreamOrigin string   `json:"upstreamOrigin,omitempty"`
	Directory      string   `json:"directory"`
	SessionCount   int      `json:"sessionCount"`
	MessageCount   int      `json:"messageCount"`
	LastUsed       int64    `json:"lastUsed"`
	TotalTokensIn  int64    `json:"totalTokensIn"`
	TotalTokensOut int64    `json:"totalTokensOut"`
	TotalCost      float64  `json:"totalCost"`
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
	SessionID        string // owning session, for per-session metric scoping
	Model            string // "provider/model"
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
