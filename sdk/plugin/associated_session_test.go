package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestAssociatedWorkspaceSessionNegotiatesAndRefusesDowngrade(t *testing.T) {
	for _, older := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "older-host"}[older], func(t *testing.T) {
			version := plugin.AssociatedSessionCapability.Version
			if older {
				version = plugin.FollowupSessionCapability.Version
			}
			enc, dec, requests, done := requestConnection(t, 2, true, plugin.AssociatedSessionCapability.Version, version)
			call, err := plugin.NewSessionRun("associated", plugin.SessionRunParams{WorkspaceID: "workspace-a", AssociatedProjectID: "platform-service", NoTools: true, Prompt: "Review"})
			if err != nil || call.Version != plugin.AssociatedSessionCapability.Version {
				t.Fatalf("association minimum version: %+v %v", call, err)
			}
			replies := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: replies}
			if older {
				if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
					t.Fatal("association silently downgraded")
				}
				call, err = plugin.NewSessionRun("compatible", plugin.SessionRunParams{WorkspaceID: "workspace-a", NoTools: true, Prompt: "Review"})
				if err != nil {
					t.Fatal(err)
				}
				requests <- plugin.Request{Call: call, Replies: replies}
			}
			message, err := dec.Decode()
			if err != nil || message.Call == nil || message.Call.Version != version {
				t.Fatalf("association wire: %+v %v", message, err)
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{"sessionId":"source-session"}`)}}); err != nil {
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
