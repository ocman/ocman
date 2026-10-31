package plugin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestToolSessionConstructorsAndHandler(t *testing.T) {
	params := plugin.SessionRunParams{WorkspaceID: "workspace-a", Prompt: "Index", Tools: &plugin.SessionTools{Context: "run-a", MaxCalls: 2, TimeoutSeconds: 30,
		Tools: []plugin.SessionTool{{Name: "read-document", Description: "Read document", InputSchema: json.RawMessage(`{"type":"object"}`)}}}}
	call, err := plugin.NewSessionRun("run-1", params)
	if err != nil || call.Version != plugin.ToolSessionCapability.Version {
		t.Fatalf("run: %+v %v", call, err)
	}
	stop, err := plugin.NewSessionStop("stop-1", "run-a")
	if err != nil || stop.Method != "stop" || stop.Version != plugin.ToolSessionCapability.Version {
		t.Fatalf("stop: %+v %v", stop, err)
	}
	if _, err := plugin.NewSessionStop("stop-2", "../escape"); err == nil {
		t.Fatal("invalid routing identity")
	}
	invoked := 0
	h := plugin.SessionToolHandler(func(_ context.Context, operationID string, p plugin.SessionToolCall) (json.RawMessage, error) {
		invoked++
		if operationID != "tool-1" || p.Context != "run-a" || p.Tool != "read-document" {
			t.Fatal(p)
		}
		return json.RawMessage(`{"text":"fixture"}`), nil
	})
	p, _ := json.Marshal(plugin.SessionToolCall{SessionID: "session-a", Context: "run-a", Tool: "read-document", Arguments: json.RawMessage(`{}`)})
	tool := plugin.Call{OperationID: "tool-1", Capability: "session", Version: plugin.ToolSessionCapability.Version, Method: plugin.SessionToolMethod, Params: p}
	if _, err := h(t.Context(), tool, nil); err != nil || invoked != 1 {
		t.Fatal(err)
	}
	tool.Method = "completed"
	if _, err := h(t.Context(), tool, nil); err == nil || invoked != 1 {
		t.Fatal("wrong method dispatched")
	}
	tool.Method = plugin.SessionToolMethod
	tool.Params = json.RawMessage(`{}`)
	if _, err := h(t.Context(), tool, nil); err == nil {
		t.Fatal("invalid tool call admitted")
	}
}

func TestSessionStopTransportAndOlderHostRefusal(t *testing.T) {
	for _, version := range []plugin.Version{plugin.AssociatedSessionCapability.Version, plugin.ToolSessionCapability.Version} {
		enc, dec, requests, done := requestConnection(t, 2, true, plugin.ToolSessionCapability.Version, version)
		call, err := plugin.NewSessionStop("stop-1", "run-a")
		if err != nil {
			t.Fatal(err)
		}
		replies := make(chan plugin.Result, 1)
		requests <- plugin.Request{Call: call, Replies: replies}
		if version.Minor < 7 {
			if r := <-replies; r.Error == nil || r.Error.Category != plugin.ErrorInvalidArgument {
				t.Fatal("stop downgraded")
			}
		} else {
			message, err := dec.Decode()
			if err != nil || message.Call == nil || message.Call.Method != "stop" {
				t.Fatalf("stop transport: %+v %v", message, err)
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{}`)}}); err != nil {
				t.Fatal(err)
			}
			if r := <-replies; r.Error != nil {
				t.Fatal(r.Error)
			}
		}
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
