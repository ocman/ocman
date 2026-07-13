package platforms

// This file defines the request/response value types passed across the
// Platform operation methods. Keeping them here (instead of in
// platform.go) lets Platform's method list read as documentation of
// supported operations.
//
// Shape rule: each operation type is a plain struct of scalars + small
// slices. Adapters translate them into whatever the backing platform
// expects. Nothing in here is agent-specific; anything platform-specific
// goes in the adapter's package.

// SendMessageRequest is a composer message submission. The caller has
// already resolved the session and its owning platform; directory is
// not on the wire because the adapter already knows it.
type SendMessageRequest struct {
	SessionID string
	Message   string
	Images    []ImageAttachment
	// Model is a "provider/model" string (platforms that don't use
	// providers may interpret it as just the model).
	Model string
	// Agent is the composer-level role (OpenCode: "build", "plan",
	// subagent name). Empty = platform default. Platforms without a
	// composer-agent concept ignore this field.
	Agent string
	// Reasoning is the model variant / thinking-budget level to use
	// (e.g. "high", "max", "low"). Empty = platform default. Only
	// meaningful when the model exposes variants.
	Reasoning string
}

// ImageAttachment is one inline image included with a composer message.
type ImageAttachment struct {
	URL  string
	Mime string
}

// ExecuteCommandRequest runs a slash command within a session.
type ExecuteCommandRequest struct {
	SessionID string
	Command   string
	Arguments string
	Model     string
	Agent     string
	Reasoning string
}

// RunShellRequest runs a raw shell command in the session's working
// directory, bypassing the LLM. Used by composers that route `!`-
// prefixed input directly to the platform's shell-tool primitive
// (e.g. OpenCode's POST /session/{id}/shell).
//
// Agent picks the role the synthesised assistant message is attributed
// to ("build" / "plan" / subagent name on OpenCode). Platforms without
// a composer-agent concept ignore it.
type RunShellRequest struct {
	SessionID string
	Command   string
	Agent     string
}

// CompactRequest compacts (summarizes) the session history.
type CompactRequest struct {
	SessionID  string
	ProviderID string
	ModelID    string
}

// RespondPermissionRequest answers a pending permission prompt.
type RespondPermissionRequest struct {
	SessionID    string
	PermissionID string
	Reply        string // "once", "always", "reject"
}

// RespondQuestionRequest submits answers to a pending question prompt.
type RespondQuestionRequest struct {
	SessionID string
	RequestID string
	Answers   [][]string
}

// RejectQuestionRequest dismisses a pending question prompt.
type RejectQuestionRequest struct {
	SessionID string
	RequestID string
}

// AbortRequest aborts an in-flight session response.
type AbortRequest struct {
	SessionID string
}

// RenameSessionRequest sets a new title for a session.
type RenameSessionRequest struct {
	SessionID string
	Title     string
}

// ForkSessionRequest branches a session into a new child session,
// optionally from a specific message in the timeline. An empty
// MessageID forks from the current HEAD.
type ForkSessionRequest struct {
	SessionID string
	MessageID string // optional; empty = fork from HEAD
}

// MoveSessionRequest relocates a session to a different project
// directory on the same host.
type MoveSessionRequest struct {
	SessionID string
	Directory string
}

// PermissionRule is one entry in a session's permission ruleset:
// which permission (tool) it applies to, a glob pattern for the
// argument, and what happens on match. Mirrors OpenCode's
// PermissionRule shape.
type PermissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"` // "allow" | "deny" | "ask"
}

// SetPermissionRulesRequest replaces a session's permission ruleset.
// An empty Rules slice restores the platform's configured defaults.
type SetPermissionRulesRequest struct {
	SessionID string
	Rules     []PermissionRule
}

// CreateSessionRequest creates a new session on the owning platform.
// Unlike the other request types, this one doesn't reference an
// existing session — the handler picks the platform from the
// ?platform= query or defaults to a single registered one.
type CreateSessionRequest struct {
	Directory string
	Title     string // Optional custom title for the new session
	// Port is an optional already-known OpenCode port. When set, the
	// adapter skips port discovery (no lsof scan) and creates the
	// session on this instance — used for worktree sessions rooted at
	// a directory other than the process launch cwd.
	Port string `json:"port,omitempty"`
}

// CreateSessionResponse is the minimal payload returned after creating
// a session — the ID the caller can now use for subsequent requests.
type CreateSessionResponse struct {
	ID string `json:"id"`
}

// AgentCatalogEntry is one entry in a platform's composer-agent
// catalog (OpenCode: "build", "plan", subagents). Platforms that don't
// have a composer-agent concept return an empty slice.
type AgentCatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	Color       string `json:"color,omitempty"`
	Kind        string `json:"kind,omitempty"` // "primary" / "subagent" / ...
	// Mode mirrors OpenCode's `mode` field ("primary" / "subagent" /
	// "all"). The frontend picker sections on this.
	Mode string `json:"mode,omitempty"`
	// Hidden marks agents OpenCode surfaces only in search (native
	// helpers like "title" / "summary" / "compaction").
	Hidden bool `json:"hidden,omitempty"`
	// BuiltIn marks OpenCode's native agents (vs. user/project agents).
	BuiltIn bool `json:"builtIn,omitempty"`
}

// SlashCommandEntry describes one slash command available to a session.
type SlashCommandEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template,omitempty"`
	// Source is the OpenCode command origin: "command", "mcp", or
	// "skill". Skills (source == "skill") back the /skills picker.
	Source string `json:"source,omitempty"`
}

// SessionModel represents one selectable model for a session.
type SessionModel struct {
	Provider          string   `json:"provider"`
	ProviderName      string   `json:"providerName,omitempty"`
	Model             string   `json:"model"`
	ModelName         string   `json:"modelName,omitempty"`
	RecentRank        int      `json:"recentRank,omitempty"`
	IsSessionDefault  bool     `json:"isSessionDefault,omitempty"`
	IsProviderDefault bool     `json:"isProviderDefault,omitempty"`
	IsAvailable       bool     `json:"isAvailable,omitempty"`
	IsFavorite        bool     `json:"isFavorite,omitempty"`
	Reasoning         []string `json:"reasoning,omitempty"`
}

// SessionModelsResponse is the full response for the session models
// endpoint.
type SessionModelsResponse struct {
	SessionDefault   string            `json:"sessionDefault,omitempty"`
	ProviderDefaults map[string]string `json:"providerDefaults,omitempty"`
	HasProviders     bool              `json:"hasProviders"`
	Models           []SessionModel    `json:"models"`
}

// LivePrompt is one pending permission/question prompt from a running
// platform instance.
type LivePrompt map[string]interface{}
