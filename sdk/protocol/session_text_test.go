package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSessionCompletedTextAndFrameBounds(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		ok         bool
	}{
		{"large JSON", `{"items":["` + strings.Repeat("x", 90_000) + `"]}`, true},
		{"unicode and escapes", " \n" + strings.Repeat("界\\\"\t", 15_000) + "\n", true},
		{"raw bound", strings.Repeat("x", SessionMaxTextBytes+1), false},
		{"envelope overhead", strings.Repeat("x", SessionMaxTextBytes), false},
		{"escaped bound", strings.Repeat("<", MaxMessageBytes/6), false},
		{"invalid UTF-8", string([]byte{0xff}), false},
		{"control", "before\x00after", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &sessionHarness{description: sessionDescription(), grants: []string{SessionRunGrant}, result: Envelope{Type: TypeResult, Result: &Result{Value: json.RawMessage(`{}`)}}}
			completion := SessionCompleted{SessionID: "session-a", Text: tc.text}
			err := h.broker().Completed(t.Context(), h.description.ID, "completion-a", completion)
			if !tc.ok {
				var wire *WireError
				if !errors.As(err, &wire) || wire.Category != ErrorInvalidArgument || len(h.calls) != 0 {
					t.Fatalf("oversized/invalid result dispatched instead of structured failure: calls=%d err=%v", len(h.calls), err)
				}
				return
			}
			if err != nil || len(h.calls) != 1 {
				t.Fatalf("valid completion rejected: %v", err)
			}
			var got SessionCompleted
			if err := json.Unmarshal(h.calls[0].Params, &got); err != nil || got.Text != tc.text {
				t.Fatal("completed text changed")
			}
		})
	}
}
