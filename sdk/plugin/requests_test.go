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

func requestConnection(t *testing.T, limit int, negotiated bool) (*plugin.Encoder, *plugin.Decoder, chan plugin.Request, <-chan error) {
	t.Helper()
	host, child := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = host.Close() })
	d := description()
	d.Scope = plugin.ScopeOwner
	d.MaxConcurrency = limit
	d.Capabilities = []plugin.Capability{plugin.SessionCapability}
	d.Actions = nil
	d.RequestedGrants = []string{plugin.SessionRunGrant}
	d.Settings = []plugin.Setting{{Key: "project", Label: "Project", Type: "string", Required: true}}
	requests := make(chan plugin.Request, 2)
	done := make(chan error, 1)
	go func() {
		h := plugin.SessionHandler(func(context.Context, string, plugin.SessionCompleted) error { return nil })
		done <- plugin.RunWithRequests(ctx, plugin.ModeServe, strings.Repeat("ab", 32), d, child, child, h, nil, requests)
	}()
	enc, dec := plugin.NewEncoder(host), plugin.NewDecoder(host)
	if _, err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	supported := d.Capabilities
	if !negotiated {
		supported = nil
	}
	n, err := plugin.Negotiate(plugin.Version{Major: 1}, supported, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &n}}); err != nil {
		t.Fatal(err)
	}
	return enc, dec, requests, done
}

func TestSessionRequestsIndependentDirections(t *testing.T) {
	enc, dec, requests, done := requestConnection(t, 2, true)
	call, err := plugin.NewSessionRun("caller-1", plugin.SessionRunParams{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	replies := make(chan plugin.Result, 1)
	requests <- plugin.Request{Call: call, Replies: replies}
	inbound, err := dec.Decode()
	if err != nil || inbound.Call == nil || inbound.Call.ID != 1 || inbound.Call.OperationID != "caller-1" {
		t.Fatalf("request: %+v %v", inbound, err)
	}
	// A simultaneous call in the opposite direction deliberately uses ID 1.
	completion := call
	completion.ID, completion.Method, completion.OperationID = 1, plugin.SessionCompletedMethod, "sess-out:1"
	completion.Params = json.RawMessage(`{"sessionId":"ses-1","text":"done"}`)
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeCall, Call: &completion}); err != nil {
		t.Fatal(err)
	}
	outbound, err := dec.Decode()
	if err != nil || outbound.Result == nil || outbound.Result.ID != 1 || string(outbound.Result.Value) != `{}` {
		t.Fatalf("completion ack: %+v %v", outbound, err)
	}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: 1, Value: json.RawMessage(`{"sessionId":"ses-1"}`)}}); err != nil {
		t.Fatal(err)
	}
	if result := <-replies; result.ID != 1 || string(result.Value) != `{"sessionId":"ses-1"}` {
		t.Fatalf("uncorrelated run reply: %+v", result)
	}
	close(requests)
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionRequestDeadlineAndShutdown(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "shutdown", true: "deadline"}[expire], func(t *testing.T) {
			enc, dec, requests, done := requestConnection(t, 2, true)
			call, err := plugin.NewSessionRun("caller-1", plugin.SessionRunParams{Prompt: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if expire {
				call.DeadlineUnixMS = time.Now().Add(30 * time.Millisecond).UnixMilli()
			}
			replies := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: replies}
			if _, err := dec.Decode(); err != nil {
				t.Fatal(err)
			}
			if expire {
				cancel, err := dec.Decode()
				if err != nil || cancel.Cancel == nil || cancel.Cancel.ID != 1 {
					t.Fatalf("cancel: %+v %v", cancel, err)
				}
				if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorDeadlineExceeded {
					t.Fatalf("deadline: %+v", result)
				}
				if err := enc.Encode(plugin.Envelope{Type: plugin.TypeResult, Result: &plugin.Result{ID: 1, Value: json.RawMessage(`{"sessionId":"late"}`)}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !expire {
				if result := <-replies; result.Error == nil || result.Error.Category != plugin.ErrorUnavailable {
					t.Fatalf("shutdown: %+v", result)
				}
			} else if len(replies) != 0 {
				t.Fatal("late result delivered a second terminal reply")
			}
		})
	}
}

func TestSessionRequestRejectsUnbufferedReplies(t *testing.T) {
	_, _, requests, done := requestConnection(t, 2, true)
	call, err := plugin.NewSessionRun("caller-1", plugin.SessionRunParams{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	requests <- plugin.Request{Call: call}
	if err := <-done; !errors.Is(err, plugin.ErrInvalidMessage) {
		t.Fatalf("accepted missing reply channel: %v", err)
	}
}

func TestSessionRequestAdmissionSettlesConsumedCaller(t *testing.T) {
	for _, negotiated := range []bool{false, true} {
		t.Run(map[bool]string{false: "unnegotiated", true: "concurrency"}[negotiated], func(t *testing.T) {
			_, dec, requests, done := requestConnection(t, 1, negotiated)
			call, err := plugin.NewSessionRun("caller-1", plugin.SessionRunParams{Prompt: "go"})
			if err != nil {
				t.Fatal(err)
			}
			first := make(chan plugin.Result, 1)
			if negotiated {
				requests <- plugin.Request{Call: call, Replies: first}
				if _, err := dec.Decode(); err != nil {
					t.Fatal(err)
				}
			}
			rejected := make(chan plugin.Result, 1)
			requests <- plugin.Request{Call: call, Replies: rejected}
			if err := <-done; !errors.Is(err, plugin.ErrStreamOrder) {
				t.Fatalf("invalid admission accepted: %v", err)
			}
			channels := []chan plugin.Result{rejected}
			if negotiated {
				channels = append(channels, first)
			}
			for _, replies := range channels {
				select {
				case result := <-replies:
					if result.Error == nil || result.Error.Category != plugin.ErrorUnavailable {
						t.Fatalf("unexpected terminal result: %+v", result)
					}
				default:
					t.Fatal("consumed caller was left without a terminal reply")
				}
			}
		})
	}
}
