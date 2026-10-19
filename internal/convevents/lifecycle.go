package convevents

import (
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Nullable is a tri-state field: absent (leave unchanged), null (clear) or a
// value. Use it with the omitzero tag option.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

func Null[T any]() Nullable[T] { return Nullable[T]{Set: true} }

func Some[T any](v T) Nullable[T] { return Nullable[T]{Set: true, Value: &v} }

func (n Nullable[T]) IsZero() bool { return !n.Set }

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*n.Value)
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set, n.Value = true, nil
	if string(b) == "null" {
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

type SessionStatusEvent struct {
	Status db.SessionStatus `json:"status"`
	// Provisional marks an idle edge whose terminal kind is not settled yet.
	Provisional bool `json:"provisional,omitempty"`
	// Notice: null clears any notice; absent leaves it unchanged.
	Notice   Nullable[db.SessionNotice] `json:"notice,omitzero"`
	ResetsAt int64                      `json:"resetsAt,omitempty"`
}

type TurnStarted struct {
	StartedAt int64  `json:"startedAt"`
	MessageID string `json:"messageId,omitempty"`
}

// Turn outcomes.
const (
	OutcomeDone        = "done"
	OutcomeError       = "error"
	OutcomeInterrupted = "interrupted"
)

type TurnEnded struct {
	Outcome         string `json:"outcome"`
	EndedAt         int64  `json:"endedAt"`
	LatestMessageID string `json:"latestMessageId,omitempty"`
}
