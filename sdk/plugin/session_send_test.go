package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
)

func TestSessionSendNegotiationAndOlderHostRefusal(t *testing.T) {
	for _, older := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "older-host"}[older], func(t *testing.T) {
			version := plugin.FollowupSessionCapability.Version
			if older {
				version = plugin.ReadOnlySessionCapability.Version
			}
			enc, dec, requests, done := requestConnection(t, 2, true, plugin.FollowupSessionCapability.Version, version)
			call, err := plugin.NewSessionSend("send-1", plugin.SessionSendParams{After: "sess-out:1", Prompt: "Clarify the supplied inventory"})
			if err != nil {
				t.Fatal(err)
			}
			replies := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: replies}
			if older {
				if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorInvalidArgument {
					t.Fatal("send downgraded")
				}
			} else {
				message, err := dec.Decode()
				if err != nil || message.Call == nil || message.Call.Method != plugin.SessionSendMethod || message.Call.Version != version {
					t.Fatalf("send failed negotiation: %+v %v", message, err)
				}
				if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: message.Call.ID, Value: json.RawMessage(`{"sessionId":"existing-session"}`)}}); err != nil {
					t.Fatal(err)
				}
				<-replies
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
