package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/sdk/plugin"
)

// RunConversationGrants exercises conversation.v1 through the canonical host
// broker. The executable must declare the capability and serve reply calls
// without side effects under a test configuration. It checks the declaration
// contract, denied and granted reply dispatch, and that an inbound normalized
// message only ever reaches the one configured project.
func RunConversationGrants(t *testing.T, executable string, project string) {
	t.Helper()
	c, d := start(t, executable, plugin.ModeServe, "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd")
	c.ready()
	if d.Validate() != nil {
		t.Fatal("description does not satisfy the conversation declaration contract")
	}
	var grants []string
	calls, started := 0, []string{}
	broker := plugins.NewConversationBroker(
		func(_ context.Context, _ string, admit func(plugins.Description, []string, string) error) error {
			return admit(d, grants, project)
		},
		func(_ context.Context, _, dir string, m plugins.ConversationMessage) error {
			started = append(started, m.AccountID+"|"+m.ThreadID+"|"+dir+"|"+m.Text)
			return nil
		},
		func(_ context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
			calls++
			if call.Capability != plugins.ConversationCapability.Name || call.Method != plugins.ConversationReplyMethod {
				t.Fatalf("unexpected call: %s/%s", call.Capability, call.Method)
			}
			id := c.call(call, time.Now().Add(time.Second))
			result, chunks := c.result(id)
			if chunks != 0 {
				t.Fatal("reply emitted chunks")
			}
			replies := make(chan plugins.Reply, 1)
			replies <- plugins.Reply{Message: plugin.Envelope{Type: plugin.TypeResult, Result: &result}}
			close(replies)
			return replies, nil
		})

	reply := plugins.ConversationReply{AccountID: "conformance", ThreadID: "conformance:1.0", Text: "conformance reply"}
	if err := broker.Reply(t.Context(), d.ID, "conformance-reply", reply); err == nil || calls != 0 {
		t.Fatalf("ungranted reply dispatched: %v", err)
	}
	grants = []string{plugins.ConversationSessionGrant}
	if err := broker.Reply(t.Context(), d.ID, "conformance-reply", reply); err != nil || calls != 1 {
		t.Fatalf("granted reply failed: %v", err)
	}

	message := plugins.ConversationMessage{
		AccountID: "conformance", ThreadID: "conformance:1.0", EventID: "ev-1", Text: "conformance prompt",
	}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	event := plugins.Event{Capability: plugins.ConversationCapability.Name, Name: plugins.ConversationMessageEvent, Data: data}
	if err := broker.Deliver(t.Context(), d.ID, event); err != nil {
		t.Fatalf("granted message denied: %v", err)
	}
	if len(started) != 1 || started[0] != "conformance|conformance:1.0|"+project+"|conformance prompt" {
		t.Fatalf("message did not start the configured project: %v", started)
	}
	message.Project = project + "-other"
	if data, err = json.Marshal(message); err != nil {
		t.Fatal(err)
	}
	event.Data = data
	if err := broker.Deliver(t.Context(), d.ID, event); err == nil || len(started) != 1 {
		t.Fatalf("unapproved project accepted: %v", err)
	}
	grants = nil
	if err := broker.Deliver(t.Context(), d.ID, plugins.Event{Capability: event.Capability, Name: event.Name, Data: json.RawMessage(`{"accountId":"conformance","threadId":"conformance:1.0","eventId":"ev-2","text":"revoked"}`)}); err == nil || len(started) != 1 {
		t.Fatalf("revoked grant still started a session: %v", err)
	}
	c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
	c.exit()
}
