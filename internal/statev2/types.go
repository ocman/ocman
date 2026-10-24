// Package statev2 owns durable, instance-scoped conversation history. It is
// independent of platform adapters and the legacy state and OpenCode databases.
package statev2

import (
	"encoding/json"
	"errors"
)

var (
	ErrNotFound         = errors.New("statev2: not found")
	ErrConflict         = errors.New("statev2: ingestion conflict")
	ErrIdentityConflict = errors.New("statev2: instance identity conflict")
)

// Instance identifies one source installation/configuration on one owner.
// ID is persistent and must not be reused for another source. SourceKey is a
// credential-free source locator, not an endpoint password or access token.
type Instance struct {
	ID        string
	OwnerID   string
	Platform  string
	SourceKey string
	Name      string
}

// Key scopes a platform-native session ID to its source instance.
type Key struct {
	InstanceID string
	SessionID  string
}

// Session contains historical metadata, never authority to run a live action.
// Revision is an Ocman ingestion revision, not a platform timestamp or cursor.
type Session struct {
	Key
	Title         string
	Directory     string
	ParentID      string // native parent session, empty for roots
	ProjectID     string
	CreatedAt     int64
	UpdatedAt     int64
	SourceDeleted bool
	Revision      int64
	Extensions    json.RawMessage
	ReadFormat    int
	// Subagent marks a helper session, decided by the adapter that produced
	// it. Session and prompt counts leave subagents out.
	Subagent bool
}

// Message is a normalized conversation envelope. Position is source-defined
// ordering, not arrival order. Accounting fields are copied out of Extensions
// so analytics can filter and sum them with indexes. Extensions preserve
// platform-specific metadata.
type Message struct {
	ID                  string
	Position            int64
	Role                string
	Agent               string // composer agent role, such as "build" or a subagent name
	Provider            string
	Model               string
	CreatedAt           int64
	CompletedAt         int64
	Cost                float64
	TokensInput         int64
	TokensOutput        int64
	TokensReasoning     int64
	TokensCacheRead     int64
	TokensCacheWrite    int64
	Finish              string
	HasError            bool
	ErrorName           string
	SourceDeleted       bool
	ConversationVisible bool
	Extensions          json.RawMessage
}

// Part is an ordered content block. Kind remains extensible so an adapter can
// retain unsupported blocks instead of silently dropping them. Structured tool
// and attachment metadata can be retained in Extensions during normalization.
type Part struct {
	ID            string
	MessageID     string
	Position      int64
	Kind          string
	Text          string
	SourceDeleted bool
	CreatedAt     int64
	TextPresent   bool
	Extensions    json.RawMessage
}

// CheckpointUpdate uses opaque cursors with compare-and-swap, never lexical or
// timestamp ordering. Stream must include a source generation when cursors reset.
// An empty ExpectedCursor means the stream has not been ingested yet.
type CheckpointUpdate struct {
	Stream         string
	ExpectedCursor string
	Cursor         string
}

// Batch applies partial upserts. Absence never deletes data. ExpectedRevision
// must match the stored session revision, or zero for a new session. An adapter
// must coordinate snapshot reads with events before constructing a repair batch;
// the revision guard rejects overlapping writes but cannot order source events.
type Batch struct {
	Session          Session
	Complete         bool // authoritative full content snapshot; omitted records become source-deleted
	ExpectedRevision int64
	Messages         []Message
	Parts            []Part
	Checkpoint       *CheckpointUpdate
}

type CommitResult struct {
	Revision  int64
	Duplicate bool
}

// Detail is one consistent, paginated snapshot of retained history.
type Detail struct {
	Session            Session
	Messages           []Message
	Parts              []Part
	TotalMessages      int
	ReadyVersion       string // validated source version, read in the same snapshot
	Tree               []Session
	Conversation       ConversationMetadata
	ConversationTotals map[string]ConversationTotals
}

type ConversationTotals struct {
	Role, Finish, Error                   string
	CompletedAt, PromptAt, ActiveDuration int64
	Messages                              int
	Input, Output                         int64
	Cost                                  float64
	Models                                []ConversationModelTotal
}

type ConversationModelTotal struct {
	Provider, Model                      string
	Input, Output, CacheRead, CacheWrite int64
	Paid, Unpaid                         float64
}

type ConversationMetadata struct {
	LatestRole          string
	LatestFinish        string
	LatestError         string
	LatestErrorName     string
	LatestErrorMessage  string
	ContextTokens       int64
	UserModel           string
	DefaultAgent        string
	DefaultModel        string
	LastTurnCompletedAt int64
	LastUserPromptAt    int64
	ActiveDurationMs    int64
}
