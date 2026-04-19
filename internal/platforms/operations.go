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

// CreateSessionRequest creates a new session on the owning platform.
// Unlike the other request types, this one doesn't reference an
// existing session — the handler picks the platform from the
// ?platform= query or defaults to a single registered one.
type CreateSessionRequest struct {
	Directory string
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
}

// SlashCommandEntry describes one slash command available to a session.
type SlashCommandEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template,omitempty"`
}

// SessionModel represents one selectable model for a session.
type SessionModel struct {
	Provider          string `json:"provider"`
	ProviderName      string `json:"providerName,omitempty"`
	Model             string `json:"model"`
	ModelName         string `json:"modelName,omitempty"`
	RecentRank        int    `json:"recentRank,omitempty"`
	IsSessionDefault  bool   `json:"isSessionDefault,omitempty"`
	IsProviderDefault bool   `json:"isProviderDefault,omitempty"`
	IsAvailable       bool   `json:"isAvailable,omitempty"`
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
