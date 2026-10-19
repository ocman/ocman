package convevents

// Prompt kinds.
const (
	PromptPermission = "permission"
	PromptQuestion   = "question"
)

type Prompt struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// SessionID is the session that asked; it may be a descendant of the
	// envelope's session.
	SessionID  string             `json:"sessionId"`
	AskedAt    int64              `json:"askedAt"`
	Permission *PermissionRequest `json:"permission,omitempty"`
	Question   *QuestionRequest   `json:"question,omitempty"`
}

type PermissionRequest struct {
	Permission string         `json:"permission"`
	Patterns   []string       `json:"patterns"`
	Always     []string       `json:"always,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type QuestionRequest struct {
	Questions []QuestionItem `json:"questions"`
}

// QuestionItem mirrors the frontend QuestionPrompt item.
type QuestionItem struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple,omitempty"`
	Custom   bool             `json:"custom,omitempty"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type PromptAsked struct {
	Prompt Prompt `json:"prompt"`
}

// Judge phases.
const (
	JudgeScheduled = "scheduled"
	JudgeChecking  = "checking"
	JudgeFlagged   = "flagged"
)

type PromptJudge struct {
	PromptID  string `json:"promptId"`
	Phase     string `json:"phase"`
	StartsAt  int64  `json:"startsAt,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// Prompt outcomes and actors.
const (
	ResolvedOnce      = "once"
	ResolvedAlways    = "always"
	ResolvedReject    = "reject"
	ResolvedAnswered  = "answered"
	ResolvedDismissed = "dismissed"
	ResolvedCancelled = "cancelled"

	ByUser     = "user"
	ByAI       = "ai"
	ByRule     = "rule"
	ByExternal = "external"
)

type PromptResolved struct {
	PromptID string `json:"promptId"`
	Kind     string `json:"kind"`
	Outcome  string `json:"outcome"`
	By       string `json:"by"`
}
