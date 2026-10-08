package db

// AgentMetrics holds request metrics grouped by composer agent.
type AgentMetrics struct {
	Agent              string  `json:"agent"`
	Requests           int     `json:"requests"`
	SuccessfulRequests int     `json:"successfulRequests"`
	ErrorRequests      int     `json:"errorRequests"`
	ErrorRate          float64 `json:"errorRate"`
	InputTokens        int64   `json:"inputTokens"`
	OutputTokens       int64   `json:"outputTokens"`
	TotalTokens        int64   `json:"totalTokens"`
	TotalDurationMs    int64   `json:"totalDurationMs"`
	EffectiveCost      float64 `json:"effectiveCost"`
	// These sum to TotalDurationMs. Tool time is the clipped union of
	// intervals in each request; incomplete timing stays unattributed.
	AgentDurationMs   int64 `json:"agentDurationMs"`
	ToolDurationMs    int64 `json:"toolDurationMs"`
	UnknownDurationMs int64 `json:"unknownDurationMs"`
}
