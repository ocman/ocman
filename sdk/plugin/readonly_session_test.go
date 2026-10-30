package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestReadOnlySourceRunUsesExplicitMinorVersion(t *testing.T) {
	call, err := plugin.NewSessionRun("source-1", plugin.SessionRunParams{
		WorkspaceID: "workspace-a", Subpath: "source", ReadOnly: true, Archived: true, Prompt: "Index prepared source",
	})
	if err != nil || call.Version != plugin.ReadOnlySessionCapability.Version {
		t.Fatalf("source mode downgraded: %+v %v", call, err)
	}
}

func TestReadOnlySourceRunDoesNotDowngradeOrBreakOlderConnections(t *testing.T) {
	enc, dec, requests, done := requestConnection(t, 2, true, plugin.ReadOnlySessionCapability.Version, plugin.ArchivedSessionCapability.Version)
	call, err := plugin.NewSessionRun("source-1", plugin.SessionRunParams{WorkspaceID: "workspace-a", Subpath: "source", ReadOnly: true, Prompt: "Index documents"})
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan plugin.Result, 1)
	requests <- plugin.Request{Call: call, Replies: replies}
	if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
		t.Fatal("source mode was downgraded")
	}
	call, err = plugin.NewSessionRun("model-1", plugin.SessionRunParams{WorkspaceID: "workspace-a", NoTools: true, Prompt: "Summarize text"})
	if err != nil {
		t.Fatal(err)
	}
	requests <- plugin.Request{Call: call, Replies: replies}
	message, err := dec.Decode()
	if err != nil || message.Call == nil || message.Call.Version != plugin.ArchivedSessionCapability.Version {
		t.Fatalf("legacy request failed: %+v %v", message, err)
	}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{"sessionId":"model-1"}`)}}); err != nil {
		t.Fatal(err)
	}
	<-replies
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
