package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRequestSafeErrors(t *testing.T) {
	for _, test := range []struct {
		err      error
		category ErrorCategory
	}{
		{context.DeadlineExceeded, ErrorDeadlineExceeded},
		{context.Canceled, ErrorCancelled},
		{&WireError{Category: ErrorNotFound}, ErrorNotFound},
		{ErrUnavailable, ErrorUnavailable},
		{ErrBusy, ErrorUnavailable},
		{errors.New("private host path"), ErrorInternal},
	} {
		var got Result
		r := Request{Call: Call{ID: 9}, respond: func(result Result) { got = result }}
		r.Respond(json.RawMessage(`{"secret":"discarded"}`), test.err)
		if got.ID != 9 || got.Error == nil || got.Error.Category != test.category || got.Value != nil {
			t.Fatalf("unsafe or uncorrelated reply: %+v", got)
		}
	}
	for _, value := range []string{`{"sessionId":"ses-1"}`, `invalid`} {
		var got Result
		r := Request{Call: Call{ID: 9}, respond: func(result Result) { got = result }}
		r.Respond(json.RawMessage(value), nil)
		if value == "invalid" {
			if got.Error == nil || got.Error.Category != ErrorInternal {
				t.Fatalf("invalid result was sent: %+v", got)
			}
		} else if got.Error != nil || string(got.Value) != value {
			t.Fatalf("valid result: %+v", got)
		}
	}
}
