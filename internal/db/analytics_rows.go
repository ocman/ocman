package db

// AnalyticsMessage is the compact accounting row retained by the Claude
// transcript index. These rows support ingestion, not a separate analytics DB.
type AnalyticsMessage struct {
	ID, SessionID string
	TimeCreated   int64
	Settled       bool
	Data          string
	// ToolTimes holds one OpenCode-shaped state.time JSON per tool call.
	// An empty string represents an unknown timing.
	ToolTimes []string
}
