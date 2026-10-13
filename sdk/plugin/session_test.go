package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func TestNewSessionRun(t *testing.T) {
	if _, err := plugin.NewSessionRun("run-1", plugin.SessionRunParams{}); err == nil {
		t.Fatal("accepted a run without a prompt")
	}
	event, err := plugin.NewSessionRun("run-1", plugin.SessionRunParams{Prompt: "build it", Subpath: "packages/app"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Capability != plugin.SessionCapability.Name || event.Method != plugin.SessionRunMethod || event.OperationID != "run-1" {
		t.Fatalf("event %+v", event)
	}
	if (plugin.Envelope{Type: plugin.TypeCall, Call: &event}).Validate() != nil {
		t.Fatal("run call is not a valid envelope body")
	}
}

func TestSessionHandler(t *testing.T) {
	completed := plugin.Call{
		OperationID: "sess-out:4", Capability: plugin.SessionCapability.Name,
		Version: plugin.SessionCapability.Version, Method: plugin.SessionCompletedMethod,
	}
	for _, tc := range []struct {
		name   string
		call   plugin.Call
		params string
		err    error
		want   plugin.ErrorCategory
	}{
		{name: "success", call: completed, params: `{"sessionId":"ses_1","text":"done"}`},
		{name: "wrong capability", call: plugin.Call{Capability: "action", Version: plugin.Version{Major: 1}, Method: "invoke"}, params: `{}`, want: plugin.ErrorNotFound},
		{name: "wrong method", call: plugin.Call{Capability: plugin.SessionCapability.Name, Version: plugin.SessionCapability.Version, Method: "other"}, params: `{}`, want: plugin.ErrorNotFound},
		{name: "invalid completion", call: completed, params: `{"sessionId":"","text":"done"}`, want: plugin.ErrorInvalidArgument},
		{name: "invalid json", call: completed, params: `{`, want: plugin.ErrorInvalidArgument},
		{name: "provider failure", call: completed, params: `{"sessionId":"ses_1","text":"done"}`, err: plugin.Failure(plugin.ErrorUnavailable), want: plugin.ErrorUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen plugin.SessionCompleted
			var seenOperation string
			h := plugin.SessionHandler(func(_ context.Context, operationID string, c plugin.SessionCompleted) error {
				seen, seenOperation = c, operationID
				return tc.err
			})
			call := tc.call
			call.Params = json.RawMessage(tc.params)
			value, err := h(context.Background(), call, nil)
			if tc.want != "" {
				if !errors.Is(err, plugin.Failure(tc.want)) {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
				return
			}
			// The operation id reaches the connector: it is what lets a
			// retried completion be recognized instead of acted on twice.
			if err != nil || string(value) != `{}` || seen.Text != "done" || seen.SessionID != "ses_1" || seenOperation != "sess-out:4" {
				t.Fatalf("%s %v %+v %q", value, err, seen, seenOperation)
			}
		})
	}
	if _, err := plugin.SessionHandler(nil)(context.Background(), completed, nil); !errors.Is(err, plugin.Failure(plugin.ErrorInternal)) {
		t.Fatal(err)
	}
}
