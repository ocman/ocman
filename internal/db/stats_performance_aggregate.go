package db

// requestRow attaches computed fields without changing the request-log API.
type requestRow struct {
	RequestLogEntry
	sessionTitle, sessionDirectory string
	estimatedCostByType            CostByType
	completed                      bool
	isError                        bool
	modelStarted                   int64
	agentDurationMs                int64
	toolDurationMs                 int64
	unknownDurationMs              int64
}
