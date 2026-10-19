// Package tasksvc manages bounded, owner-local asynchronous Ocman work and
// durable progress records. It never persists closures or replays work on restart.
package tasksvc

import (
	"context"
	"errors"
)

var (
	ErrUnavailable = errors.New("background task manager unavailable")
	ErrClosed      = errors.New("background task manager is stopping")
	ErrBusy        = errors.New("background task already active")
	ErrNotFound    = errors.New("background task not found")
	ErrProgress    = errors.New("invalid background task progress")
)

type Status string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Cancelled   Status = "cancelled"
	Interrupted Status = "interrupted"
)

type Spec struct {
	Kind  string `json:"kind"`
	Key   string `json:"key,omitempty"` // optional active-task deduplication within Kind
	Title string `json:"title"`
	Scope string `json:"scope,omitempty"` // credential-free resource identity, not command input
}

// Progress counts completed units, including failed/skipped units. Total nil
// means unknown, not zero. Completion of a scan is separate from its success.
type Progress struct {
	Phase     string           `json:"phase"`
	Completed int64            `json:"completed"`
	Total     *int64           `json:"total"`
	Unit      string           `json:"unit,omitempty"`
	Counters  map[string]int64 `json:"counters"`
}

type Task struct {
	ID string `json:"id"`
	Spec
	OwnerID    string   `json:"ownerId"`
	Status     Status   `json:"status"`
	Progress   Progress `json:"progress"`
	CreatedAt  int64    `json:"createdAt"`
	StartedAt  int64    `json:"startedAt,omitempty"`
	UpdatedAt  int64    `json:"updatedAt"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type Work func(context.Context, *Reporter) error

// Runner lets producers use the manager without importing its HTTP module.
type Runner interface {
	Do(context.Context, Spec, Work) error
}

func ValidStatus(status Status) bool {
	switch status {
	case Queued, Running, Succeeded, Failed, Cancelled, Interrupted:
		return true
	}
	return false
}
