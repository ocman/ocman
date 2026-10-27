package convevents

import "github.com/NoUseFreak/ocman/internal/db"

// QueuedMessage mirrors queuemod.MessageView (frontend QueuedMessage).
type QueuedMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	HasImages bool   `json:"hasImages"`
	Model     string `json:"model,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type QueueChanged struct {
	Messages []QueuedMessage `json:"messages"`
}

// BackgroundTask is one session-owned background task.
type BackgroundTask = db.BackgroundTask

type BackgroundChanged struct {
	Count int              `json:"count"`
	Tasks []BackgroundTask `json:"tasks"`
}

// Child summarises a descendant session row.
type Child struct {
	ID          string `json:"id"`
	ParentID    string `json:"parentId,omitempty"`
	Title       string `json:"title"`
	TimeCreated int64  `json:"timeCreated"`
	TimeUpdated int64  `json:"timeUpdated"`
	Agent       string `json:"agent,omitempty"`
	// Deleted tells the consumer to drop the child.
	Deleted bool `json:"deleted,omitempty"`
}

type ChildUpserted struct {
	Child Child `json:"child"`
}

type ChildStatus struct {
	ChildID string                     `json:"childId"`
	Status  db.SessionStatus           `json:"status"`
	Notice  Nullable[db.SessionNotice] `json:"notice,omitzero"`
}

type RevertMarker struct {
	MessageID string `json:"messageId"`
	PartID    string `json:"partId,omitempty"`
}

// SessionPatch holds the changed session metadata; absent fields are
// unchanged. Revert null clears the marker.
type SessionPatch struct {
	Title            *string                `json:"title,omitempty"`
	SummaryAdditions *int                   `json:"summaryAdditions,omitempty"`
	SummaryDeletions *int                   `json:"summaryDeletions,omitempty"`
	SummaryFiles     *int                   `json:"summaryFiles,omitempty"`
	ShareURL         *string                `json:"shareUrl,omitempty"`
	Archived         *bool                  `json:"archived,omitempty"`
	DefaultAgent     string                 `json:"defaultAgent,omitempty"`
	DefaultModel     string                 `json:"defaultModel,omitempty"`
	Revert           Nullable[RevertMarker] `json:"revert,omitzero"`
}

type SessionUpdated struct {
	Patch SessionPatch `json:"patch"`
}

// History rewrite reasons.
const (
	RewriteCompaction = "compaction"
	RewriteRevert     = "revert"
	RewriteFork       = "fork"
	RewriteResync     = "resync"
)

type HistoryRewritten struct {
	Reason string `json:"reason"`
	// FromMessageID is the oldest message that may have changed; empty
	// means the whole session.
	FromMessageID string `json:"fromMessageId,omitempty"`
}

type HistoryCommitted struct {
	Revision int64 `json:"revision"`
}

// Changes dirty reasons.
const (
	DirtyEdit   = "edit"
	DirtyDiff   = "diff"
	DirtyCommit = "commit"
)

type ChangesDirty struct {
	Reason string `json:"reason"`
}

type ErrorInfo struct {
	Name       string `json:"name,omitempty"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode,omitempty"`
}

type SessionError struct {
	MessageID string    `json:"messageId,omitempty"`
	Error     ErrorInfo `json:"error"`
}
