package plugin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func description() plugin.Description {
	return plugin.Description{ID: "org.example.test", Name: "Test", Version: "1", Protocol: plugin.Version{Major: 1, Minor: 2}, MaxConcurrency: 2, Scope: plugin.ScopeHub,
		Capabilities:    []plugin.Capability{{Name: "action", Version: plugin.Version{Major: 1}}},
		RequestedGrants: []string{"context.session"},
		Actions:         []plugin.ActionDescriptor{{ID: "test", Label: "Test", Placement: "session", RequiredGrants: []string{"context.session"}, Surfaces: []string{"command-palette"}}}}
}

func TestActionHandler(t *testing.T) {
	for _, tc := range []struct {
		name, params, method string
		results              []plugin.ActionResult
		err                  error
		want                 plugin.ErrorCategory
	}{
		{name: "success-additive", params: `{"actionId":"test","context":{"sessionId":"ses-1","future":true},"future":true}`, results: []plugin.ActionResult{{Kind: "notice", Text: "OK"}}},
		{name: "undeclared-context", params: `{"actionId":"test","context":{"ownerId":"local"}}`, want: plugin.ErrorPermissionDenied},
		{name: "invalid-context", params: `{"actionId":"test","context":{"sessionId":"/tmp/secret"}}`, want: plugin.ErrorInvalidArgument},
		{name: "invalid-json", params: `{`, want: plugin.ErrorInvalidArgument},
		{name: "missing-action", params: `{"actionId":"absent"}`, want: plugin.ErrorNotFound},
		{name: "unknown-method", method: "unknown", params: `{}`, want: plugin.ErrorNotFound},
		{name: "unsafe-result", params: `{"actionId":"test"}`, results: []plugin.ActionResult{{Kind: "link", Label: "Unsafe", URL: "javascript:alert(1)"}}, want: plugin.ErrorInternal},
		{name: "handler-error", params: `{"actionId":"test"}`, err: plugin.Failure(plugin.ErrorConflict), want: plugin.ErrorConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := plugin.ActionHandler(description(), func(context.Context, plugin.Call, plugin.ActionInvocation) ([]plugin.ActionResult, error) {
				return tc.results, tc.err
			})
			method := tc.method
			if method == "" {
				method = "invoke"
			}
			value, err := h(context.Background(), plugin.Call{Capability: "action", Version: plugin.Version{Major: 1}, Method: method, Params: json.RawMessage(tc.params)}, func(json.RawMessage) error { t.Fatal("action emitted chunk"); return nil })
			if tc.want != "" {
				if !errors.Is(err, plugin.Failure(tc.want)) {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plugin.DecodeActionResults(value); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, d := range []plugin.Description{{}, description()} {
		_, err := plugin.ActionHandler(d, nil)(context.Background(), plugin.Call{}, nil)
		if !errors.Is(err, plugin.Failure(plugin.ErrorInternal)) {
			t.Fatal(err)
		}
	}
}

func session(t *testing.T, h plugin.Handler) (*plugin.Encoder, *plugin.Decoder, <-chan error, context.CancelFunc) {
	t.Helper()
	host, child := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = host.Close() })
	done := make(chan error, 1)
	go func() {
		done <- plugin.Run(ctx, plugin.ModeServe, strings.Repeat("ab", 32), description(), child, child, h)
	}()
	decoder := plugin.NewDecoder(host)
	e, err := decoder.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if e.Hello.Token != strings.Repeat("ab", 32) {
		t.Fatal("wrong binding")
	}
	return plugin.NewEncoder(host), decoder, done, cancel
}

func TestServeErrorsAndNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		value string
		want  plugin.ErrorCategory
	}{
		{"internal", errors.New("secret"), "", plugin.ErrorInternal},
		{"unknown-category", plugin.Failure("secret"), "", plugin.ErrorInternal},
		{"known-category", plugin.Failure(plugin.ErrorUnavailable), "", plugin.ErrorUnavailable},
		{"invalid-result", nil, "not-json", plugin.ErrorInternal},
		{"cancelled", context.Canceled, "", plugin.ErrorCancelled},
		{"deadline", context.DeadlineExceeded, "", plugin.ErrorDeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, dec, done, _ := session(t, func(context.Context, plugin.Call, func(json.RawMessage) error) (json.RawMessage, error) {
				return json.RawMessage(tc.value), tc.err
			})
			n, err := plugin.Negotiate(plugin.Version{Major: 1}, description().Capabilities, description())
			if err != nil {
				t.Fatal(err)
			}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &n}}); err != nil {
				t.Fatal(err)
			}
			call := plugin.Call{ID: 1, OperationID: "op", Capability: "action", Version: plugin.Version{Major: 1}, Method: "invoke", DeadlineUnixMS: time.Now().Add(time.Second).UnixMilli(), Params: json.RawMessage(`{}`)}
			if err := enc.Encode(plugin.Envelope{Type: plugin.TypeCall, Call: &call}); err != nil {
				t.Fatal(err)
			}
			e, err := dec.Decode()
			if err != nil || e.Result == nil || e.Result.Error == nil || e.Result.Error.Category != tc.want {
				t.Fatalf("%+v %v", e, err)
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

func TestServeRejectsHandshake(t *testing.T) {
	for _, ack := range []plugin.Envelope{
		{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}},
		{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &plugin.Negotiated{Protocol: plugin.Version{Major: 2}}}},
		{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &plugin.Negotiated{Protocol: plugin.Version{Major: 1, Minor: 3}}}},
		{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &plugin.Negotiated{Protocol: plugin.Version{Major: 1}, Capabilities: []plugin.Capability{{Name: "unknown", Version: plugin.Version{Major: 1}}}}}},
	} {
		enc, _, done, _ := session(t, nil)
		if err := enc.Encode(ack); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err == nil {
			t.Fatal("accepted invalid handshake")
		}
	}
}

func TestServeContextClosesBlockedRead(t *testing.T) {
	_, _, done, cancel := session(t, nil)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked handshake")
	}
}

func TestRejectedChunkDoesNotConsumeSequence(t *testing.T) {
	enc, dec, done, _ := session(t, func(_ context.Context, _ plugin.Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		if err := emit(json.RawMessage(`invalid`)); err == nil {
			return nil, errors.New("invalid chunk accepted")
		}
		if err := emit(json.RawMessage(`{"ok":true}`)); err != nil {
			return nil, err
		}
		return json.RawMessage(`null`), nil
	})
	n, _ := plugin.Negotiate(plugin.Version{Major: 1}, description().Capabilities, description())
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &n}}); err != nil {
		t.Fatal(err)
	}
	call := plugin.Call{ID: 1, OperationID: "op", Capability: "action", Version: plugin.Version{Major: 1}, Method: "test", DeadlineUnixMS: time.Now().Add(time.Second).UnixMilli(), Params: json.RawMessage(`{}`)}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeCall, Call: &call}); err != nil {
		t.Fatal(err)
	}
	e, err := dec.Decode()
	if err != nil || e.Chunk == nil || e.Chunk.Sequence != 1 {
		t.Fatalf("chunk: %+v, %v", e, err)
	}
	if e, err := dec.Decode(); err != nil || e.Result == nil || e.Result.Error != nil {
		t.Fatalf("result: %+v, %v", e, err)
	}
	if err := enc.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProtocolAliases(t *testing.T) {
	token := strings.Repeat("ab", 32)
	s, err := plugin.NewStream(plugin.ModeDescribe, token, plugin.Version{Major: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := description()
	if err := s.Accept(plugin.FromPlugin, plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeDescribe, Token: strings.Repeat("cd", 32), Description: &d}}); !errors.Is(err, plugin.ErrHandshake) {
		t.Fatal(err)
	}
	if _, err := plugin.Negotiate(plugin.Version{Major: 2}, nil, d); !errors.Is(err, plugin.ErrIncompatibleVersion) {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"type":"shutdown","shutdown":{},"future":true}` + "\n",
		`{"type":"shutdown","shutdown":{"future":true}}` + "\r\n",
	} {
		if _, err := plugin.NewDecoder(strings.NewReader(body)).Decode(); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`{"type":"shutdown","shutdown":{}}`, "\n", `{"type":"shutdown","type":"shutdown","shutdown":{}}` + "\n"} {
		if _, err := plugin.NewDecoder(strings.NewReader(body)).Decode(); err == nil {
			t.Fatal("accepted invalid framing")
		}
	}
	var b bytes.Buffer
	if err := plugin.NewEncoder(&b).Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
		t.Fatal(err)
	}
	dec := plugin.NewDecoder(&b)
	if _, err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
