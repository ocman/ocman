package codexappserver

import "encoding/json"

// The types below are the subset of the app-server v2 API ocman uses. Field
// names follow codex-rs/app-server-protocol/schema/typescript/v2. Unknown
// fields are ignored and open-ended enums are kept as strings, so newer
// servers decode without changes.

// Client request methods.
const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodThreadStart   = "thread/start"
	MethodThreadResume  = "thread/resume"
	MethodThreadFork    = "thread/fork"
	MethodThreadRead    = "thread/read"
	MethodThreadTurns   = "thread/turns/list"
	MethodThreadItems   = "thread/items/list"
	MethodThreadList    = "thread/list"
	MethodThreadArchive = "thread/archive"
	MethodThreadNameSet = "thread/name/set"
	MethodThreadCompact = "thread/compact/start"
	MethodTurnStart     = "turn/start"
	MethodTurnSteer     = "turn/steer"
	MethodTurnInterrupt = "turn/interrupt"
	MethodModelList     = "model/list"
	MethodShellCommand  = "thread/shellCommand"
	MethodReviewStart   = "review/start"
	MethodSkillsList    = "skills/list"
	MethodAccountRead   = "account/read"
	MethodRateLimits    = "account/rateLimits/read"
)

// Server notifications.
const (
	NotifyThreadStarted       = "thread/started"
	NotifyThreadStatusChanged = "thread/status/changed"
	NotifyThreadNameUpdated   = "thread/name/updated"
	NotifyTokenUsageUpdated   = "thread/tokenUsage/updated"
	NotifyTurnStarted         = "turn/started"
	NotifyTurnCompleted       = "turn/completed"
	NotifyItemStarted         = "item/started"
	NotifyItemCompleted       = "item/completed"
	NotifyAgentMessageDelta   = "item/agentMessage/delta"
	NotifyReasoningSummary    = "item/reasoning/summaryTextDelta"
	NotifyReasoningText       = "item/reasoning/textDelta"
	NotifyCommandOutputDelta  = "item/commandExecution/outputDelta"
	NotifyServerRequestDone   = "serverRequest/resolved"
	NotifyError               = "error"
	NotifyRateLimitsUpdated   = "account/rateLimits/updated"
)

// Server-to-client requests.
const (
	RequestCommandApproval    = "item/commandExecution/requestApproval"
	RequestFileChangeApproval = "item/fileChange/requestApproval"
	RequestUserInput          = "item/tool/requestUserInput"
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

// ThreadStartParams starts a thread. Sandbox is a SandboxMode string
// ("read-only", "workspace-write", "danger-full-access"); ApprovalPolicy is
// "untrusted", "on-request" or "never".
type ThreadStartParams struct {
	Model                 string         `json:"model,omitempty"`
	ModelProvider         string         `json:"modelProvider,omitempty"`
	Cwd                   string         `json:"cwd,omitempty"`
	ApprovalPolicy        string         `json:"approvalPolicy,omitempty"`
	Sandbox               string         `json:"sandbox,omitempty"`
	Config                map[string]any `json:"config,omitempty"`
	DeveloperInstructions string         `json:"developerInstructions,omitempty"`
	Ephemeral             bool           `json:"ephemeral,omitempty"`
}

type ThreadResumeParams struct {
	ThreadID       string         `json:"threadId"`
	Model          string         `json:"model,omitempty"`
	Cwd            string         `json:"cwd,omitempty"`
	ApprovalPolicy string         `json:"approvalPolicy,omitempty"`
	Sandbox        string         `json:"sandbox,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
	ExcludeTurns   bool           `json:"excludeTurns,omitempty"`
}

// ThreadForkParams forks through LastTurnID, inclusive; empty forks the
// whole thread. ExcludeTurns skips the deprecated full-history response.
type ThreadForkParams struct {
	ThreadID     string `json:"threadId"`
	LastTurnID   string `json:"lastTurnId,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	ExcludeTurns bool   `json:"excludeTurns,omitempty"`
}

// ShellCommandParams runs a user shell command as its own turn. It runs
// unsandboxed with full access, like a terminal.
type ShellCommandParams struct {
	ThreadID string `json:"threadId"`
	Command  string `json:"command"`
}

// ReviewStartParams starts an inline review turn. Target Type is
// "uncommittedChanges", "baseBranch" (Branch), "commit" (SHA) or "custom"
// (Instructions).
type ReviewStartParams struct {
	ThreadID string       `json:"threadId"`
	Target   ReviewTarget `json:"target"`
}

type ReviewTarget struct {
	Type         string `json:"type"`
	Branch       string `json:"branch,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Instructions string `json:"instructions,omitempty"`
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
	} `json:"account"`
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

// ThreadResponse is the common result of thread/start, thread/resume and
// thread/fork.
type ThreadResponse struct {
	Thread          Thread `json:"thread"`
	Model           string `json:"model"`
	ModelProvider   string `json:"modelProvider"`
	Cwd             string `json:"cwd"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
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

type ThreadIDParams struct {
	ThreadID string `json:"threadId"`
}

type ThreadNameSetParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

// UserInput is one turn input. Type is "text", "image" (URL) or "localImage"
// (Path). TextElements is snake_case on the wire, unlike its siblings.
type UserInput struct {
	Type         string            `json:"type"`
	Text         string            `json:"text,omitempty"`
	TextElements []json.RawMessage `json:"text_elements,omitempty"`
	URL          string            `json:"url,omitempty"`
	Path         string            `json:"path,omitempty"`
	Name         string            `json:"name,omitempty"`
}

// TextInput is a plain text input.
func TextInput(text string) UserInput {
	return UserInput{Type: "text", Text: text}
}

// MarshalJSON always sends text_elements on text inputs: the server requires
// the list even when it is empty, and omitempty would drop it.
func (u UserInput) MarshalJSON() ([]byte, error) {
	type plain UserInput
	if u.Type != "text" {
		return json.Marshal(plain(u))
	}
	elements := u.TextElements
	if elements == nil {
		elements = []json.RawMessage{}
	}
	return json.Marshal(struct {
		plain
		TextElements []json.RawMessage `json:"text_elements"`
	}{plain(u), elements})
}

type TurnStartParams struct {
	ThreadID            string      `json:"threadId"`
	Input               []UserInput `json:"input"`
	ClientUserMessageID string      `json:"clientUserMessageId,omitempty"`
	Cwd                 string      `json:"cwd,omitempty"`
	ApprovalPolicy      string      `json:"approvalPolicy,omitempty"`
	Model               string      `json:"model,omitempty"`
	Effort              string      `json:"effort,omitempty"`
}

type TurnSteerParams struct {
	ThreadID            string      `json:"threadId"`
	Input               []UserInput `json:"input"`
	ExpectedTurnID      string      `json:"expectedTurnId"`
	ClientUserMessageID string      `json:"clientUserMessageId,omitempty"`
}

type TurnResponse struct {
	Turn Turn `json:"turn"`
}

type TurnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
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
