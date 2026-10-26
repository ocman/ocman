package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func TestArchivedSessionNegotiates13AndFailsClosedOnOlderHosts(t *testing.T) {
	for _, older := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "older-host"}[older], func(t *testing.T) {
			version := plugin.ArchivedSessionCapability.Version
			if older {
				version = plugin.PluginSessionCapability.Version
			}
			enc, dec, requests, done := requestConnection(t, 2, true, plugin.ArchivedSessionCapability.Version, version)
			call, err := plugin.NewSessionRun("background", plugin.SessionRunParams{Prompt: "verify", Archived: true})
			if err != nil || call.Version != plugin.ArchivedSessionCapability.Version {
				t.Fatalf("archived minimum version: %+v %v", call, err)
			}
			replies := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: replies}
			if older {
				if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
					t.Fatal("archived parameter downgraded")
				}
				if call, err = plugin.NewSessionRun("visible", plugin.SessionRunParams{Prompt: "verify"}); err != nil {
					t.Fatal(err)
				}
				requests <- plugin.Request{Call: call, Replies: replies}
			}
			message, err := dec.Decode()
			if err != nil || message.Call == nil || message.Call.Version != version {
				t.Fatalf("archived wire: %+v %v", message, err)
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{"sessionId":"bg"}`)}}); err != nil {
				t.Fatal(err)
			}
			<-replies
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
