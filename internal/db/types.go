package db

import "encoding/json"

// Session represents an OpenCode session.
type Session struct {
	ID                string  `json:"id"`
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
	Status            string  `json:"status"`  // "waiting", "busy", "done", or "error"
	HasPort           bool    `json:"hasPort"` // true if a running OpenCode instance with --port is detected
	Archived          bool    `json:"archived"`
	Seen              bool    `json:"seen"`
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
type MessageData struct {
	Role       string     `json:"role"`
	Agent      string     `json:"agent"`
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

// PartData is the parsed data from a part.
type PartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Tool string `json:"tool"`
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
