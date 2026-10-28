package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestWorkspaceSessionNegotiates12AndFailsClosedOnOlderHosts(t *testing.T) {
	for _, older := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "older-host"}[older], func(t *testing.T) {
			version := plugin.PluginSessionCapability.Version
			if older {
				version = plugin.ProjectSessionCapability.Version
			}
			enc, dec, requests, done := requestConnection(t, 2, true, plugin.PluginSessionCapability.Version, version)
			call, err := plugin.NewSessionRun("private-model", plugin.SessionRunParams{WorkspaceID: "workspace-a", NoTools: true, Prompt: "judge"})
			if err != nil || call.Version != plugin.PluginSessionCapability.Version {
				t.Fatalf("workspace minimum version: %+v %v", call, err)
			}
			replies := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: replies}
			if older {
				result := <-replies
				if result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
					t.Fatal("workspace parameter downgraded")
				}
				call, err = plugin.NewSessionRun("compatible", plugin.SessionRunParams{NoTools: true, Prompt: "judge"})
				if err != nil {
					t.Fatal(err)
				}
				requests <- plugin.Request{Call: call, Replies: replies}
			}
			message, err := dec.Decode()
			if err != nil || message.Call == nil || message.Call.Version != version {
				t.Fatalf("workspace wire: %+v %v", message, err)
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{"sessionId":"model-turn"}`)}}); err != nil {
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
