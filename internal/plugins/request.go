package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

// Request is a plugin-initiated unary call. Context ends at its wire deadline
// or when the process connection closes. Respond never targets a later launch.
type Request struct {
	Call    Call
	Context context.Context
	respond func(Result)
}

func (r Request) Respond(value json.RawMessage, err error) {
	result := Result{ID: r.Call.ID, Value: value}
	if err != nil {
		result.Value = nil
		result.Error = RequestError(err)
	}
	if (Envelope{Type: TypeResult, Result: &result}).Validate() != nil {
		result.Value, result.Error = nil, &WireError{Category: ErrorInternal}
	}
	r.respond(result)
}

// RequestError emits only safe categories, never a host path or error text.
func RequestError(err error) *WireError {
	var wire *WireError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &WireError{Category: ErrorDeadlineExceeded}
	case errors.Is(err, context.Canceled):
		return &WireError{Category: ErrorCancelled}
	case errors.As(err, &wire):
		return wire
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrBusy):
		return &WireError{Category: ErrorUnavailable}
	default:
		return &WireError{Category: ErrorInternal}
	}
}
