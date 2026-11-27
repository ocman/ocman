package plugin_test

import (
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestChildRunDoesNotDowngrade(t *testing.T) {
	enc, _, requests, done := requestConnection(t, 2, true, plugin.ChildSessionCapability.Version, plugin.StreamingSessionCapability.Version)
	call, err := plugin.NewSessionRun("child", plugin.SessionRunParams{
		WorkspaceID: "review", ParentRun: "coordinator", NoTools: true, Prompt: "Review this slice",
	})
	if err != nil || call.Version != plugin.ChildSessionCapability.Version {
		t.Fatalf("wrong minimum version: %v %v", call.Version, err)
	}
	replies := make(chan plugin.Result, 1)
	requests <- plugin.Request{Call: call, Replies: replies}
	if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
		t.Fatal("child request was not rejected by the older connection")
	}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
