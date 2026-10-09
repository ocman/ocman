package db

// SessionStatus is the platform-agnostic lifecycle state. Keep the TypeScript
// SessionStatus union in sync with this closed set.
type SessionStatus string

const (
	StatusBusy        SessionStatus = "busy"
	StatusWaiting     SessionStatus = "waiting"
	StatusDone        SessionStatus = "done"
	StatusError       SessionStatus = "error"
	StatusInterrupted SessionStatus = "interrupted"
)

func (s SessionStatus) String() string { return string(s) }

// TurnState comes from the agent's live lifecycle, not stored message shape.
type TurnState int

const (
	TurnUnobserved TurnState = iota
	TurnRunning
	TurnSettled
)

// SettleSessionStatus is the single rule combining live lifecycle and stored
// inference. Only the agent can say a turn is running. An absent agent with
// an unfinished turn is interrupted; stored messages identify terminal states.
func SettleSessionStatus(turn TurnState, live bool, inferred SessionStatus) SessionStatus {
	switch turn {
	case TurnRunning:
		return StatusBusy
	case TurnSettled:
		if inferred == StatusBusy {
			return StatusDone
		}
		return inferred
	case TurnUnobserved:
		if inferred == StatusBusy && !live {
			return StatusInterrupted
		}
		return inferred
	}
	return inferred
}

// InferSessionStatus identifies terminal states from the last message. Busy
// here means unfinished, not running; feed it to SettleSessionStatus.
// synthesizedTerminal identifies completed non-LLM envelopes such as shell
// commands: parts exist, none starts an LLM step, and none is running.
func InferSessionStatus(lastRole, lastFinish, lastError string, synthesizedTerminal bool) SessionStatus {
	if lastRole == "assistant" {
		if lastFinish == "error" || lastError != "" {
			return StatusError
		}
		if lastFinish != "" {
			return StatusWaiting
		}
		if synthesizedTerminal {
			return StatusDone
		}
		return StatusBusy
	}
	return StatusDone
}

// Session represents a coding-platform session. Platform identifies the tool,
// not the composer agent role. Unsupported platform fields are zero or nil.
type Session struct {
	ID        string `json:"id"`
	Platform  string `json:"platform"`
	ProjectID string `json:"projectId"`
	// ParentID is the native parent, or an ocman child link stamped by the server.
	ParentID    string `json:"parentId,omitempty"`
	Title       string `json:"title"`
	Directory   string `json:"directory"`
	TimeCreated int64  `json:"timeCreated"`
	TimeUpdated int64  `json:"timeUpdated"`
	// Latest terminal assistant turn, including errors. Streaming does not advance it.
	LastTurnCompletedAt int64   `json:"lastTurnCompletedAt"`
	LastUserPromptAt    int64   `json:"lastUserPromptAt"`
	LastHaltAt          int64   `json:"lastHaltAt"`
	SummaryAdditions    *int    `json:"summaryAdditions"`
	SummaryDeletions    *int    `json:"summaryDeletions"`
	SummaryFiles        *int    `json:"summaryFiles"`
	ShareURL            *string `json:"shareUrl"`
	MessageCount        int     `json:"messageCount"`
	DurationMs          int64   `json:"durationMs"`
	// Sum of assistant message completed-created times; excludes user think time.
	ActiveDurationMs   int64   `json:"activeDurationMs"`
	TotalInputTokens   int64   `json:"totalInputTokens"`
	TotalOutputTokens  int64   `json:"totalOutputTokens"`
	TotalCost          float64 `json:"totalCost"`
	TotalEstCost       float64 `json:"totalEstCost"`
	TotalEffectiveCost float64 `json:"totalEffectiveCost"`
	// Always produced by SettleSessionStatus, never raw inference alone.
	Status            SessionStatus `json:"status"`
	LiveConnection    bool          `json:"liveConnection"`
	PendingPermission bool          `json:"pendingPermission"`
	PendingQuestion   bool          `json:"pendingQuestion"`
	Archived          bool          `json:"archived"`
	Seen              bool          `json:"seen"`
	Pinned            bool          `json:"pinned"`
	PinnedAt          int64         `json:"pinnedAt"`
	RoutineID         string        `json:"routineId,omitempty"`
	FactoryAttemptID  string        `json:"factoryAttemptId,omitempty"`
	// Session time_updated when viewed. The server stamps this read watermark.
	SeenTimeUpdated int64 `json:"seenTimeUpdated"`
	// Messages newer than the read watermark; zero when suppressed or unsupported.
	UnreadCount         int            `json:"unreadCount"`
	Notice              *SessionNotice `json:"notice,omitempty"`
	ProjectDefaultModel string         `json:"projectDefaultModel,omitempty"`
	// Display-only owner attributes. Capabilities control host behavior.
	RemoteID   string `json:"remoteId,omitempty"`
	RemoteName string `json:"remoteName,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
	// Internal error metadata for the notice normalizer, never serialized.
	LastErrorName    string `json:"-"`
	LastErrorMessage string `json:"-"`
	LastErrorAt      int64  `json:"-"`
}

// SessionNotice explains transient conditions without exposing platform details.
type SessionNotice struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	RetryAt int64  `json:"retryAt"`
	Attempt int    `json:"attempt"`
}

type SessionArchiveCandidate struct {
	ID          string
	TimeUpdated int64
}
