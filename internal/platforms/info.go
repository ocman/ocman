package platforms

// SessionInfo is the response shape for the session-scoped "info"
// snapshot consumed by the right-hand "Session info" panel.
//
// Two tiers of data:
//
//   - Always populated when the platform supports the call:
//     Tokens (lifetime input/output/cache totals) and Todos (latest
//     recognisable todowrite list). Both come from data the adapter
//     already has on disk and are independent of any live agent
//     process.
//
//   - Only populated when `Supported=true`: ContextInfo.Limit, the
//     MCPServers list, and the LSPServers list. These require a live
//     channel to a running agent (e.g. an OpenCode --port). When the
//     live channel is unavailable, `Supported=false` and these fields
//     are zero/empty.
//
// Adapters that don't expose any of this return
// ErrUnsupported from Platform.SessionInfo; the HTTP layer translates
// that into a Supported=false payload with empty slices.
type SessionInfo struct {
	SessionID  string      `json:"sessionId"`
	Supported  bool        `json:"supported"`
	Context    ContextInfo `json:"context"`
	Tokens     TokenTotals `json:"tokens"`
	MCPServers []MCPServer `json:"mcpServers"`
	LSPServers []LSPServer `json:"lspServers"`
	// Messages is the user/assistant turn breakdown for the session.
	// Both zero when the adapter doesn't compute it (the existing
	// /api/sessions `messageCount` field counts only user messages,
	// so the panel needs a richer view to show "user + agent").
	Messages MessageCounts `json:"messages"`
	// Todos is the most recent recognisable todo list from the
	// session's `todowrite` tool calls, or nil/empty when none. The
	// adapter walks the session's parts newest-to-oldest, so this
	// always reflects the conversation's current task tracker rather
	// than a slice limited by message pagination.
	Todos []TodoItem `json:"todos,omitempty"`
}

// MessageCounts is the user/assistant turn breakdown surfaced by the
// SessionInfo panel. Both fields are zero when the platform doesn't
// emit the breakdown — the frontend falls back to the legacy single
// `messageCount` value in that case.
//
// "Assistant" mirrors the OpenCode role label and is
// rendered as "agent" in the UI; the wire format keeps the role-
// vocabulary name for parity with the underlying message data.
type MessageCounts struct {
	User      int64 `json:"user"`
	Assistant int64 `json:"assistant"`
}

// TokenTotals breaks the session's lifetime token usage into the four
// buckets the SessionInfo panel surfaces. Cache read/write are not in
// the existing /api/sessions wire payload, so the SessionInfo path is
// the only place a client can reliably read them — the SQL summary
// query in db.GetSessions only sums input + output.
type TokenTotals struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// TodoItem mirrors the todowrite tool's per-
// row shape: a content string, a lifecycle status ("pending",
// "in_progress", "completed"), and an optional priority hint.
//
// Adapters MUST emit the upstream values verbatim. The frontend
// styles known statuses ("completed" gets struck through, etc.) and
// renders unknown values neutrally so a future status string
// surfaces without a server change.
type TodoItem struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// ContextInfo is the context-window usage snapshot. Tokens is the
// running count attributed to the most recent assistant turn (same
// value surfaced as SessionDetail.ContextTokenCount). Limit is the
// model's context-window size in tokens — 0 when unknown (live model
// catalog unreachable, model not in catalog, etc.).
//
// Cost and EstCost are reported independently:
//
//   - Cost is the sum of the platform-recorded `cost` field across
//     assistant messages — what was actually billed. Zero for
//     subscription-plan accounts whose messages all record cost=0.
//
//   - EstCost is a token-derived estimate computed locally from the
//     loaded pricing table, ignoring the upstream cost field. It's
//     the only meaningful number for subscription-plan sessions and
//     a useful sanity check against Cost on API-priced sessions.
//     Zero when no pricing table is wired.
//
// The frontend renders both as separate rows; consumers shouldn't
// pick one over the other server-side.
type ContextInfo struct {
	Tokens  int64   `json:"tokens"`
	Limit   int64   `json:"limit,omitempty"`
	Cost    float64 `json:"cost"`
	EstCost float64 `json:"estCost"`
	// Model is the "provider/model" reference the limit was looked
	// up under. Empty when no model has been used yet (zero-message
	// session) or when the lookup failed. Surfaced so the UI can
	// optionally render which model the % used is computed against.
	Model string `json:"model,omitempty"`
}

// MCPServer is one configured MCP (Model Context Protocol) server
// plus its current connection status.
//
// Status uses the upstream platform's vocabulary verbatim — the
// frontend renders it as-is and applies styling per known value.
// Common values for OpenCode are "connected", "needs_auth", and
// "failed". Adapters MUST NOT translate; new platforms can introduce
// new status strings without backend changes.
//
// Error is an optional human-readable explanation surfaced when the
// upstream attaches one (e.g. "Failed to get tools" on a failed
// remote MCP). Empty when no error message is available.
//
// AuthHint is an optional platform-specific command the user can run
// to authenticate the server (e.g. "opencode mcp auth <name>").
// Adapters set it when Status indicates authentication is required;
// the frontend renders it verbatim so it never has to branch on
// platform (AD-12a).
type MCPServer struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	AuthHint string `json:"authHint,omitempty"`
}

// LSPServer is one configured LSP plus its current status. ID is the
// platform-supplied stable identifier (e.g. "gopls"); Name is the
// human-friendly label (often equal to ID). Status follows the same
// "verbatim from upstream" rule as MCPServer.Status.
type LSPServer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
