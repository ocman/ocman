package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func conversationDescription() plugin.Description {
	return plugin.Description{ID: "org.example.chat", Name: "Chat", Version: "1", Protocol: plugin.Version{Major: 1}, MaxConcurrency: 2, Scope: plugin.ScopeOwner,
		Capabilities:    []plugin.Capability{plugin.ConversationCapability},
		RequestedGrants: []string{plugin.ConversationSessionGrant},
		Settings:        []plugin.Setting{{Key: plugin.ConversationProjectSetting, Label: "Project", Type: "string", Required: true}}}
}

func TestNewConversationMessage(t *testing.T) {
	if _, err := plugin.NewConversationMessage(plugin.ConversationMessage{ThreadID: "bad thread", Text: "hi"}); err == nil {
		t.Fatal("accepted an invalid message")
	}
	event, err := plugin.NewConversationMessage(plugin.ConversationMessage{ThreadID: "C1:1.0", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Capability != plugin.ConversationCapability.Name || event.Name != plugin.ConversationMessageEvent {
		t.Fatalf("event %+v", event)
	}
	if (plugin.Envelope{Type: plugin.TypeEvent, Event: &event}).Validate() != nil {
		t.Fatal("event is not a valid envelope body")
	}
}

func TestConversationHandler(t *testing.T) {
	reply := plugin.Call{Capability: plugin.ConversationCapability.Name, Version: plugin.ConversationCapability.Version, Method: plugin.ConversationReplyMethod}
	for _, tc := range []struct {
		name   string
		call   plugin.Call
		params string
		err    error
		want   plugin.ErrorCategory
	}{
		{name: "success", call: reply, params: `{"threadId":"C1:1.0","text":"done"}`},
		{name: "wrong capability", call: plugin.Call{Capability: "action", Version: plugin.Version{Major: 1}, Method: "invoke"}, params: `{}`, want: plugin.ErrorNotFound},
		{name: "wrong method", call: plugin.Call{Capability: plugin.ConversationCapability.Name, Version: plugin.ConversationCapability.Version, Method: "other"}, params: `{}`, want: plugin.ErrorNotFound},
		{name: "invalid reply", call: reply, params: `{"threadId":"","text":"done"}`, want: plugin.ErrorInvalidArgument},
		{name: "invalid json", call: reply, params: `{`, want: plugin.ErrorInvalidArgument},
		{name: "provider failure", call: reply, params: `{"threadId":"C1:1.0","text":"done"}`, err: plugin.Failure(plugin.ErrorUnavailable), want: plugin.ErrorUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen plugin.ConversationReply
			h := plugin.ConversationHandler(func(_ context.Context, r plugin.ConversationReply) error {
				seen = r
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
			if err != nil || string(value) != `{}` || seen.Text != "done" {
				t.Fatalf("%s %v %+v", value, err, seen)
			}
		})
	}
	if _, err := plugin.ConversationHandler(nil)(context.Background(), reply, nil); !errors.Is(err, plugin.Failure(plugin.ErrorInternal)) {
		t.Fatal(err)
	}
}

// conversationSession runs RunWithEvents over a pipe and returns the host side
// after the plugin's hello.
func conversationSession(t *testing.T, events <-chan plugin.Event) (*plugin.Encoder, *plugin.Decoder, <-chan error) {
	t.Helper()
	host, child := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = host.Close() })
	done := make(chan error, 1)
	handler := plugin.ConversationHandler(func(context.Context, plugin.ConversationReply) error { return nil })
	go func() {
		done <- plugin.RunWithEvents(ctx, plugin.ModeServe, strings.Repeat("ab", 32), conversationDescription(), child, child, handler, events)
	}()
	decoder := plugin.NewDecoder(host)
	if _, err := decoder.Decode(); err != nil {
		t.Fatal(err)
	}
	return plugin.NewEncoder(host), decoder, done
}

func TestRunWithEvents(t *testing.T) {
	message, err := plugin.NewConversationMessage(plugin.ConversationMessage{ThreadID: "C1:1.0", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("negotiated capability is emitted", func(t *testing.T) {
		events := make(chan plugin.Event, 1)
		enc, dec, done := conversationSession(t, events)
		n, err := plugin.Negotiate(plugin.Version{Major: 1}, conversationDescription().Capabilities, conversationDescription())
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &n}}); err != nil {
			t.Fatal(err)
		}
		events <- message
		e, err := dec.Decode()
		if err != nil || e.Event == nil || e.Event.Name != plugin.ConversationMessageEvent {
			t.Fatalf("%+v %v", e, err)
		}
		// A closed source must not end the run.
		close(events)
		call := plugin.Call{ID: 1, OperationID: "op", Capability: plugin.ConversationCapability.Name, Version: plugin.ConversationCapability.Version,
			Method: plugin.ConversationReplyMethod, DeadlineUnixMS: time.Now().Add(time.Second).UnixMilli(), Params: json.RawMessage(`{"threadId":"C1:1.0","text":"done"}`)}
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeCall, Call: &call}); err != nil {
			t.Fatal(err)
		}
		if e, err := dec.Decode(); err != nil || e.Result == nil || e.Result.Error != nil {
			t.Fatalf("%+v %v", e, err)
		}
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("undernegotiated capability is dropped", func(t *testing.T) {
		events := make(chan plugin.Event, 1)
		enc, dec, done := conversationSession(t, events)
		// Host accepted no capability: the event must be dropped, not fatal.
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &plugin.Negotiated{Protocol: plugin.Version{Major: 1}}}}); err != nil {
			t.Fatal(err)
		}
		events <- message
		if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := dec.Decode(); err == nil {
			t.Fatal("dropped event still reached the host")
		}
	})
}
