package codexappserver

// The types below are the subset of the app-server v2 API ocman uses. Field
// names follow codex-rs/app-server-protocol/schema/typescript/v2. Unknown
// fields are ignored and open-ended enums are kept as strings, so newer
// servers decode without changes.

// Client request methods. ocman only reads threads, models, skills and the
// account, and sets thread names; ACP owns execution.
const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodThreadRead    = "thread/read"
	MethodThreadTurns   = "thread/turns/list"
	MethodThreadItems   = "thread/items/list"
	MethodThreadList    = "thread/list"
	MethodThreadNameSet = "thread/name/set"
	MethodModelList     = "model/list"
	MethodSkillsList    = "skills/list"
	MethodAccountRead   = "account/read"
	MethodRateLimits    = "account/rateLimits/read"
)

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type InitializeCapabilities struct {
	ExperimentalAPI           bool     `json:"experimentalApi"`
	RequestAttestation        bool     `json:"requestAttestation"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

type InitializeResponse struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

type SkillsListParams struct {
	Cwds []string `json:"cwds,omitempty"`
}

type SkillsListResponse struct {
	Data []SkillsEntry `json:"data"`
}

type SkillsEntry struct {
	Cwd    string  `json:"cwd"`
	Skills []Skill `json:"skills"`
}

type Skill struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	ShortDescription string `json:"shortDescription,omitempty"`
	Path             string `json:"path"`
	Scope            string `json:"scope"`
	Enabled          bool   `json:"enabled"`
}

// AccountResponse's Account is nil when signed out. Type is "apiKey",
// "chatgpt" (with PlanType) or "amazonBedrock".
type AccountResponse struct {
	Account *struct {
		Type     string `json:"type"`
		PlanType string `json:"planType,omitempty"`
		Email    string `json:"email,omitempty"`
	} `json:"account"`
	RequiresOpenaiAuth *bool `json:"requiresOpenaiAuth,omitempty"`
}

type RateLimitsResponse struct {
	RateLimits RateLimitSnapshot `json:"rateLimits"`
}

type RateLimitSnapshot struct {
	LimitID   string           `json:"limitId,omitempty"`
	Primary   *RateLimitWindow `json:"primary"`
	Secondary *RateLimitWindow `json:"secondary"`
	PlanType  string           `json:"planType,omitempty"`
}

// RateLimitWindow ResetsAt is Unix seconds.
type RateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type ThreadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns,omitempty"`
}

type ThreadReadResponse struct {
	Thread Thread `json:"thread"`
}

// ThreadTurnsListParams pages a thread's turns. ItemsView is "notLoaded",
// "summary" (the server default) or "full"; SortDirection defaults to "desc".
type ThreadTurnsListParams struct {
	ThreadID      string `json:"threadId"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
	ItemsView     string `json:"itemsView,omitempty"`
}

// ThreadItemsListParams pages a thread's items, oldest first by default.
type ThreadItemsListParams struct {
	ThreadID      string `json:"threadId"`
	TurnID        string `json:"turnId,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
}

// ItemEntry is a stored item with the turn it belongs to. Stored items carry
// their timestamps here rather than on the item.
type ItemEntry struct {
	TurnID        string `json:"turnId"`
	Item          Item   `json:"item"`
	StartedAtMs   int64  `json:"startedAtMs,omitempty"`
	CompletedAtMs int64  `json:"completedAtMs,omitempty"`
}

// ThreadListParams pages stored threads. SourceKinds defaults to interactive
// sources on the server; pass every kind to include subagent threads.
type ThreadListParams struct {
	Cursor        string   `json:"cursor,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	SortKey       string   `json:"sortKey,omitempty"`
	SortDirection string   `json:"sortDirection,omitempty"`
	SourceKinds   []string `json:"sourceKinds,omitempty"`
	Archived      *bool    `json:"archived,omitempty"`
	Cwd           string   `json:"cwd,omitempty"`
}

// AllSourceKinds lists every ThreadSourceKind, so listing includes the
// subagent threads the server hides by default.
var AllSourceKinds = []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview",
	"subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}

type ThreadNameSetParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

// UserInput is one stored user message input. Type is "text", "image" (URL)
// or "localImage" (Path).
type UserInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

type ModelListParams struct {
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	IncludeHidden bool   `json:"includeHidden,omitempty"`
}

type Model struct {
	ID                        string            `json:"id"`
	Model                     string            `json:"model"`
	DisplayName               string            `json:"displayName"`
	Description               string            `json:"description"`
	Hidden                    bool              `json:"hidden"`
	IsDefault                 bool              `json:"isDefault"`
	DefaultReasoningEffort    string            `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []ReasoningEffort `json:"supportedReasoningEfforts"`
}

type ReasoningEffort struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}
