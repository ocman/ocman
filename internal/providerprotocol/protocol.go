// Package providerprotocol defines ocman's internal HTTP + SSE backend wire
// contract. Its only ocman dependencies are the shared session and platform DTOs.
package providerprotocol

import (
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const (
	// CurrentVersion 4 supports adopting externally started Claude sessions.
	// Older surviving supervisors must drain rather than reject the new sends.
	CurrentVersion = 4
	// PreviousVersion is the oldest supervisor reused after an upgrade.
	// Version 1 (raw patterns, no reply_unrecorded) and 2 (acknowledged
	// lifecycle replayed as new), and 3 (no external adoption) drain and are
	// replaced once idle.
	PreviousVersion = CurrentVersion
	// ExactPatternsVersion is the first version whose prompt patterns are
	// ExactPattern-escaped. A draining version-1 supervisor sends raw tool
	// arguments; the adapter escapes them.
	ExactPatternsVersion = 2
	// ReplyOutcomeVersion is the first version that reports a reply the
	// agent received but could not record (CodeReplyUnrecorded) instead of
	// a generic 503.
	ReplyOutcomeVersion = 2
	MaxEventBytes       = 8 << 20
)

type Hello struct {
	ProtocolVersion int `json:"protocolVersion"`
	// PlatformID identifies the coding tool, not its model provider.
	// The legacy wire key stays providerId for surviving supervisors.
	PlatformID   string       `json:"providerId"`
	CLIVersion   string       `json:"cliVersion"`
	Capabilities Capabilities `json:"capabilities"`
	// IdleDrainToken lets a new owner recover an interrupted cleanup pause.
	IdleDrainToken string `json:"idleDrainToken,omitempty"`
}

type Capabilities struct {
	platforms.Capabilities
	NativeQueue    bool `json:"nativeQueue"`
	Rename         bool `json:"rename"`
	Account        bool `json:"account"`
	Replay         bool `json:"replay"`                   // explicit turn acknowledgments and leased snapshots
	Hooks          bool `json:"hooks"`                    // POST /hooks: status of sessions started outside ocman
	HistoryConfirm bool `json:"historyConfirm,omitempty"` // optional durable-history receipts for transient views
	Ownership      bool `json:"ownership,omitempty"`      // loaded-thread ownership probes, without launching a runtime
}

// SessionOwnership proves a thread is loaded in a supervisor's native runtime.
type SessionOwnership struct {
	SourceKey string `json:"sourceKey"`
	Loaded    bool   `json:"loaded"`
	Idle      bool   `json:"idle"`
}

const CodeSessionOwnedElsewhere = "session_owned_elsewhere"

type HistoryConfirmation struct {
	Entries map[string]string `json:"entries"` // message presence and semantic part digests, keyed by native IDs
}

// HookEvent is one CLI hook (e.g. Stop) reported for a session the backend
// does not drive. Optional process identity supports best-effort owner warnings.
type HookEvent struct {
	Event     string `json:"event"`
	SessionID string `json:"sessionID"`
	PID       int    `json:"pid,omitempty"`
	Managed   bool   `json:"managed,omitempty"`
}

// Snapshot contains current v1 upserts, not deltas. Lease pins events after
// Cursor until /events claims it. Expired leases return 409; take a new snapshot.
type Snapshot struct {
	Cursor uint64  `json:"cursor"`
	Lease  string  `json:"lease"`
	Events []Event `json:"events"`
}

type TurnAck struct {
	Sequence uint64 `json:"sequence"` // exact session.idle sequence for this turn
}

type DrainState struct {
	Idle bool `json:"idle"`
}

// VersionError lets runtime management distinguish drain-and-replace from an
// unreachable socket. No running turn is interrupted by protocol negotiation.
type VersionError struct{ Version int }

func (e *VersionError) Error() string {
	return fmt.Sprintf("unsupported provider protocol version %d (supported: %d-%d)", e.Version, PreviousVersion, CurrentVersion)
}

func CheckVersion(version int) error {
	if version < PreviousVersion || version > CurrentVersion {
		return &VersionError{Version: version}
	}
	return nil
}

// Event is an OpenCode v1 event. SSE id, outside this JSON payload, is the
// backend-wide monotonically increasing sequence used by /events?after=.
type Event struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	// Acknowledged marks a lifecycle event (session.status, session.idle)
	// of a turn the headless consumer already acknowledged, replayed only
	// because the spool still retains it (a late prompt resolution): a
	// consumer rebuilds state from it but runs no turn-end side effects.
	Acknowledged bool `json:"acknowledged,omitempty"`
}

type Account struct {
	SubscriptionType string      `json:"subscriptionType"`
	RateLimits       []RateLimit `json:"rateLimits"`
}

type RateLimit struct {
	Name        string  `json:"name"`        // e.g. five_hour or weekly
	Utilization float64 `json:"utilization"` // fraction, 0..1
	ResetsAt    int64   `json:"resetsAt"`    // Unix milliseconds
}

// CodeReplyUnrecorded marks a 503 prompt reply the backend delivered but
// could not record yet (platforms.ErrReplyUnrecorded).
const CodeReplyUnrecorded = "reply_unrecorded"

// CodeCreationRejected guarantees that create/fork failed before allocation.
// Missing this code leaves the outcome uncertain, regardless of HTTP status.
const CodeCreationRejected = "creation_rejected"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Aliases keep stored, live, local and remote session shapes identical.
type Session = db.Session
type SessionDetail = platforms.SessionDetail
type SessionLifecycle = platforms.SessionLifecycle
type CreateSessionRequest = platforms.CreateSessionRequest
type CreateSessionResponse = platforms.CreateSessionResponse
type ForkSessionRequest = platforms.ForkSessionRequest
type RenameSessionRequest = platforms.RenameSessionRequest
type CompactRequest = platforms.CompactRequest
type AbortRequest = platforms.AbortRequest
type SendMessageRequest = platforms.SendMessageRequest
type ExecuteCommandRequest = platforms.ExecuteCommandRequest
type RunShellRequest = platforms.RunShellRequest
type LivePrompt = platforms.LivePrompt
type RespondPermissionRequest = platforms.RespondPermissionRequest
type RespondQuestionRequest = platforms.RespondQuestionRequest
type RejectQuestionRequest = platforms.RejectQuestionRequest
type PermissionRule = platforms.PermissionRule
type SetPermissionRulesRequest = platforms.SetPermissionRulesRequest
type DirectoryCatalog = platforms.DirectoryCatalog
type NativeQueuedMessage = platforms.NativeQueuedMessage
type CancelNativeQueuedRequest = platforms.CancelNativeQueuedRequest
