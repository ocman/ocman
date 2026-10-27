package codexappserver

import "encoding/json"

// Thread is a stored or loaded conversation. CreatedAt and UpdatedAt are Unix
// seconds. Path is the rollout file, marked unstable upstream.
type Thread struct {
	ID              string        `json:"id"`
	SessionID       string        `json:"sessionId"`
	ForkedFromID    string        `json:"forkedFromId,omitempty"`
	ParentThreadID  string        `json:"parentThreadId,omitempty"`
	Preview         string        `json:"preview"`
	Ephemeral       bool          `json:"ephemeral"`
	HistoryMode     string        `json:"historyMode"`
	ModelProvider   string        `json:"modelProvider"`
	Model           string        `json:"model,omitempty"`
	ReasoningEffort string        `json:"reasoningEffort,omitempty"`
	CreatedAt       int64         `json:"createdAt"`
	UpdatedAt       int64         `json:"updatedAt"`
	Status          ThreadStatus  `json:"status"`
	Path            string        `json:"path,omitempty"`
	Cwd             string        `json:"cwd"`
	CLIVersion      string        `json:"cliVersion"`
	Source          SessionSource `json:"source"`
	AgentNickname   string        `json:"agentNickname,omitempty"`
	AgentRole       string        `json:"agentRole,omitempty"`
	GitInfo         *GitInfo      `json:"gitInfo,omitempty"`
	Name            string        `json:"name,omitempty"`
	Turns           []Turn        `json:"turns"`
}

// SessionSource is a string ("cli", "appServer", ...) or a single-key object
// ({"subAgent": ...}, {"custom": ...}). Kind is the string or the object key.
type SessionSource struct {
	Kind string
	Raw  json.RawMessage
}

func (s *SessionSource) UnmarshalJSON(data []byte) error {
	s.Raw = append(s.Raw[:0], data...)
	var kind string
	if json.Unmarshal(data, &kind) == nil {
		s.Kind = kind
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for k := range obj {
		s.Kind = k
	}
	return nil
}

func (s SessionSource) MarshalJSON() ([]byte, error) {
	if len(s.Raw) > 0 {
		return s.Raw, nil
	}
	return json.Marshal(s.Kind)
}

type GitInfo struct {
	SHA       string `json:"sha,omitempty"`
	Branch    string `json:"branch,omitempty"`
	OriginURL string `json:"originUrl,omitempty"`
}

// ThreadStatus Type is "notLoaded", "idle", "systemError" or "active".
// ActiveFlags holds "waitingOnApproval" and "waitingOnUserInput".
type ThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

// Turn statuses.
const (
	TurnCompleted   = "completed"
	TurnInterrupted = "interrupted"
	TurnFailed      = "failed"
	TurnInProgress  = "inProgress"
)

// Turn times are Unix seconds.
type Turn struct {
	ID          string     `json:"id"`
	Items       []Item     `json:"items"`
	Status      string     `json:"status"`
	Error       *TurnError `json:"error,omitempty"`
	StartedAt   int64      `json:"startedAt,omitempty"`
	CompletedAt int64      `json:"completedAt,omitempty"`
	DurationMs  int64      `json:"durationMs,omitempty"`
}

// TurnError's CodexErrorInfo is a string or a single-key object; it is kept
// raw because its variants are open-ended.
type TurnError struct {
	Message           string          `json:"message"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo,omitempty"`
	AdditionalDetails string          `json:"additionalDetails,omitempty"`
}

// Item types.
const (
	ItemUserMessage       = "userMessage"
	ItemAgentMessage      = "agentMessage"
	ItemPlan              = "plan"
	ItemReasoning         = "reasoning"
	ItemCommandExecution  = "commandExecution"
	ItemFileChange        = "fileChange"
	ItemMcpToolCall       = "mcpToolCall"
	ItemDynamicToolCall   = "dynamicToolCall"
	ItemWebSearch         = "webSearch"
	ItemImageView         = "imageView"
	ItemContextCompaction = "contextCompaction"
	ItemEnteredReview     = "enteredReviewMode"
	ItemExitedReview      = "exitedReviewMode"
)

// Item is the flat ThreadItem union, tagged by Type. Only the fields of the
// variants ocman renders are decoded; Raw keeps the full payload.
type Item struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	StartedAtMs   int64  `json:"startedAtMs,omitempty"`
	CompletedAtMs int64  `json:"completedAtMs,omitempty"`

	// userMessage
	Content []UserInput `json:"content,omitempty"`
	// agentMessage, plan
	Text  string `json:"text,omitempty"`
	Phase string `json:"phase,omitempty"`
	// reasoning
	Summary          []string `json:"summary,omitempty"`
	ReasoningContent []string `json:"-"`
	// commandExecution, fileChange, mcpToolCall, dynamicToolCall
	Status           string  `json:"status,omitempty"`
	Command          string  `json:"command,omitempty"`
	Source           string  `json:"source,omitempty"` // commandExecution: "agent", "userShell", ...
	Cwd              string  `json:"cwd,omitempty"`
	AggregatedOutput *string `json:"aggregatedOutput,omitempty"`
	ExitCode         *int    `json:"exitCode,omitempty"`
	DurationMs       *int64  `json:"durationMs,omitempty"`
	// fileChange
	Changes []FileUpdate `json:"changes,omitempty"`
	// mcpToolCall, dynamicToolCall
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	ToolError *ToolError      `json:"error,omitempty"`
	// webSearch
	Query string `json:"query,omitempty"`
	// enteredReviewMode, exitedReviewMode
	Review string `json:"review,omitempty"`
	// imageView
	Path string `json:"path,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the item and keeps its raw payload. reasoning reuses
// the "content" key for strings, while userMessage uses it for inputs.
func (it *Item) UnmarshalJSON(data []byte) error {
	type plain Item
	var probe struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	var p plain
	if probe.Type == ItemReasoning {
		var r struct {
			plain
			Content []string `json:"content"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		p = r.plain
		p.ReasoningContent = r.Content
	} else if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*it = Item(p)
	it.Raw = append(json.RawMessage(nil), data...)
	return nil
}

type FileUpdate struct {
	Path string         `json:"path"`
	Kind FileUpdateKind `json:"kind"`
	Diff string         `json:"diff"`
}

// FileUpdateKind Type is "add", "delete" or "update".
type FileUpdateKind struct {
	Type     string `json:"type"`
	MovePath string `json:"move_path,omitempty"`
}

type ToolError struct {
	Message string `json:"message"`
}

// Notification payloads.

type ThreadNotification struct {
	Thread Thread `json:"thread"`
}

type ThreadStatusNotification struct {
	ThreadID string       `json:"threadId"`
	Status   ThreadStatus `json:"status"`
}

type ThreadNameNotification struct {
	ThreadID   string `json:"threadId"`
	ThreadName string `json:"threadName,omitempty"`
}

type TurnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type ItemNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     Item   `json:"item"`
}

// DeltaNotification carries streamed text for agent messages, reasoning and
// command output.
type DeltaNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	SummaryIndex *int   `json:"summaryIndex,omitempty"`
	ContentIndex *int   `json:"contentIndex,omitempty"`
}

type TokenUsageNotification struct {
	ThreadID   string           `json:"threadId"`
	TurnID     string           `json:"turnId"`
	TokenUsage ThreadTokenUsage `json:"tokenUsage"`
}

type ThreadTokenUsage struct {
	Total              TokenUsage `json:"total"`
	Last               TokenUsage `json:"last"`
	ModelContextWindow *int64     `json:"modelContextWindow,omitempty"`
}

type TokenUsage struct {
	TotalTokens           int64 `json:"totalTokens"`
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
}

type ErrorNotification struct {
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	Error     TurnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
}

type ServerRequestResolved struct {
	ThreadID  string          `json:"threadId"`
	RequestID json.RawMessage `json:"requestId"`
}

// Approval requests and replies.

// Approval decisions accepted by both approval requests.
const (
	DecisionAccept           = "accept"
	DecisionAcceptForSession = "acceptForSession"
	DecisionDecline          = "decline"
	DecisionCancel           = "cancel"
)

type CommandApprovalRequest struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Reason   string `json:"reason,omitempty"`
	Command  string `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
}

type FileChangeApprovalRequest struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Reason    string `json:"reason,omitempty"`
	GrantRoot string `json:"grantRoot,omitempty"`
}

// ApprovalResponse answers either approval request with a plain decision.
type ApprovalResponse struct {
	Decision string `json:"decision"`
}

type UserInputRequest struct {
	ThreadID   string          `json:"threadId"`
	TurnID     string          `json:"turnId"`
	ItemID     string          `json:"itemId"`
	Questions  []InputQuestion `json:"questions"`
	IsBlocking bool            `json:"isBlocking"`
}

type InputQuestion struct {
	ID       string        `json:"id"`
	Header   string        `json:"header"`
	Question string        `json:"question"`
	IsOther  bool          `json:"isOther"`
	IsSecret bool          `json:"isSecret"`
	Options  []InputOption `json:"options,omitempty"`
}

type InputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// UserInputResponse maps question IDs to their answers.
type UserInputResponse struct {
	Answers map[string]InputAnswer `json:"answers"`
}

type InputAnswer struct {
	Answers []string `json:"answers"`
}
