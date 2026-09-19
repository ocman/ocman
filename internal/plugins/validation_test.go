package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEnvelopeValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Envelope){
		"missing type":      func(e *Envelope) { e.Type = "" },
		"extra body":        func(e *Envelope) { e.Cancel = &Cancel{ID: 1} },
		"mode":              func(e *Envelope) { e.Hello.Mode = "other" },
		"short token":       func(e *Envelope) { e.Hello.Token = "secret" },
		"nonhex token":      func(e *Envelope) { e.Hello.Token = strings.Repeat("g", 64) },
		"uppercase token":   func(e *Envelope) { e.Hello.Token = strings.ToUpper(testToken) },
		"id":                func(e *Envelope) { e.Hello.Description.ID = "not-reverse-domain" },
		"name":              func(e *Envelope) { e.Hello.Description.Name = "\nsecret" },
		"version":           func(e *Envelope) { e.Hello.Description.Version = "" },
		"process major":     func(e *Envelope) { e.Hello.Description.Protocol.Major = 0 },
		"process minor":     func(e *Envelope) { e.Hello.Description.Protocol.Minor = -1 },
		"zero concurrency":  func(e *Envelope) { e.Hello.Description.MaxConcurrency = 0 },
		"large concurrency": func(e *Envelope) { e.Hello.Description.MaxConcurrency = MaxConcurrency + 1 },
		"scope":             func(e *Envelope) { e.Hello.Description.Scope = "remote" },
		"duplicate capability": func(e *Envelope) {
			e.Hello.Description.Capabilities = append(e.Hello.Description.Capabilities, e.Hello.Description.Capabilities[0])
		},
		"capability name":    func(e *Envelope) { e.Hello.Description.Capabilities[0].Name = "" },
		"capability version": func(e *Envelope) { e.Hello.Description.Capabilities[0].Version = Version{} },
	} {
		t.Run(name, func(t *testing.T) {
			e := hello(ModeServe)
			mutate(&e)
			var b bytes.Buffer
			if err := NewEncoder(&b).Encode(e); !errors.Is(err, ErrInvalidMessage) || b.Len() != 0 {
				t.Fatalf("got %v, wrote %d bytes", err, b.Len())
			}
		})
	}
	for name, e := range map[string]Envelope{
		"empty":          {},
		"empty call":     {Type: TypeCall, Call: &Call{}},
		"empty chunk":    chunk(0, 0),
		"empty result":   {Type: TypeResult, Result: &Result{ID: 1}},
		"invalid result": {Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(`{`)}},
		"mixed result":   {Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(`null`), Error: &WireError{Category: ErrorInternal}}},
		"unknown error":  {Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: "secret"}}},
		"empty event":    {Type: TypeEvent, Event: &Event{}},
		"empty cancel":   cancel(0),
	} {
		t.Run(name, func(t *testing.T) {
			if err := e.Validate(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatal(err)
			}
		})
	}
	for _, category := range []ErrorCategory{ErrorInvalidArgument, ErrorPermissionDenied, ErrorNotFound, ErrorConflict, ErrorUnavailable, ErrorDeadlineExceeded, ErrorCancelled, ErrorInternal} {
		e := Envelope{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: category}}}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []Scope{ScopeHub, ScopeOwner, ScopeGlobal} {
		e := hello(ModeServe)
		e.Hello.Description.Scope = scope
		e.Hello.Description.Capabilities = nil
		var b bytes.Buffer
		if err := NewEncoder(&b).Encode(e); err != nil {
			t.Fatal(err)
		}
		if _, err := NewDecoder(&b).Decode(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMalformedNestedFields(t *testing.T) {
	for _, input := range []string{
		`{"type":"cancel","cancel":[]}`,
		`{"type":"cancel","cancel":{"id":null}}`,
		`{"type":"hello","hello":{"description":{"capabilities":{}}}}`,
		`{"type":"hello","hello":{"description":{"capabilities":[null]}}}`,
		`{"type":"shutdown","shutdown":{},"extra":{"x":1,"x":2}}`,
		`{"type":"shutdown","shutdown":{},"extra":[1,]}`,
		`{"type":"shutdown","shutdown":{},"extra":{"x":}}`,
		`{"type":"shutdown","shutdown":{},"extra":{1:2}}`,
		`{"type":"shutdown","shutdown":{},"extra":[}`,
		`{"type":"shutdown","shutdown":{},"extra":{"x":1]}`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := NewDecoder(strings.NewReader(input + "\n")).Decode(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatal(err)
			}
		})
	}
	e := result(1)
	e.Result.Value = json.RawMessage(`{"duplicate":1,"duplicate":2}`)
	if err := NewEncoder(io.Discard).Encode(e); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal(err)
	}
}

func TestStreamValidation(t *testing.T) {
	for name, e := range map[string]Envelope{
		"invalid envelope":         {},
		"unnegotiated capability":  {Type: TypeEvent, Event: &Event{Capability: "unknown", Name: "changed", Data: json.RawMessage(`null`)}},
		"unnegotiated call":        func() Envelope { e := call(1); e.Call.Capability = "unknown"; return e }(),
		"wrong capability version": func() Envelope { e := call(1); e.Call.Version.Minor = 1; return e }(),
	} {
		t.Run(name, func(t *testing.T) {
			s := readyStream(t)
			direction := FromHost
			if e.Type == TypeEvent {
				direction = FromPlugin
			}
			if err := s.Accept(direction, e); err == nil {
				t.Fatal("accepted invalid message")
			}
			if err := s.Accept(FromHost, call(1)); !errors.Is(err, ErrStreamOrder) {
				t.Fatal("stream not terminal")
			}
		})
	}
	for _, tc := range []struct {
		direction Direction
		e         Envelope
	}{
		{FromHost, chunk(1, 1)}, {FromHost, result(1)}, {FromPlugin, cancel(1)},
		{FromPlugin, Envelope{Type: TypeShutdown, Shutdown: &Shutdown{}}},
		{FromHost, Envelope{Type: TypeEvent, Event: &Event{Capability: "action", Name: "changed", Data: json.RawMessage(`null`)}}},
		{Direction(42), call(2)},
	} {
		s := readyStream(t)
		if err := s.Accept(FromHost, call(1)); err != nil {
			t.Fatal(err)
		}
		if err := s.Accept(tc.direction, tc.e); err == nil {
			t.Fatalf("accepted %s from %d", tc.e.Type, tc.direction)
		}
	}
	s := readyStream(t)
	if err := s.Accept(FromPlugin, Envelope{Type: TypeEvent, Event: &Event{Capability: "action", Name: "changed", Data: json.RawMessage(`null`)}}); err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id < 5; id++ {
		if err := s.Accept(FromHost, call(id)); err != nil {
			t.Fatal(err)
		}
		if err := s.Accept(FromPlugin, result(id)); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.active) != 0 {
		t.Fatal("completed requests retained")
	}
}

func readyStream(t *testing.T) *Stream {
	t.Helper()
	s, err := NewStream(ModeServe, testToken, Version{1, 0}, []Capability{{"action", Version{1, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(FromPlugin, hello(ModeServe)); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(FromHost, acknowledgment(s)); err != nil {
		t.Fatal(err)
	}
	return s
}

func acknowledgment(s *Stream) Envelope {
	n, _ := s.Negotiation()
	return Envelope{Type: TypeHello, Hello: &Hello{Mode: ModeServe, Accepted: &n}}
}

func TestAcknowledgment(t *testing.T) {
	for name, mutate := range map[string]func(*Envelope){
		"valid":              func(*Envelope) {},
		"wrong protocol":     func(e *Envelope) { e.Hello.Accepted.Protocol.Major++ },
		"wrong capabilities": func(e *Envelope) { e.Hello.Accepted.Capabilities = []Capability{{"other", Version{1, 0}}} },
		"invalid protocol":   func(e *Envelope) { e.Hello.Accepted.Protocol.Major = 0 },
		"reused token":       func(e *Envelope) { e.Hello.Token = testToken },
		"wrong mode":         func(e *Envelope) { e.Hello.Mode = ModeDescribe },
		"missing selection":  func(e *Envelope) { e.Hello.Accepted = nil },
		"mixed hello":        func(e *Envelope) { e.Hello.Description = hello(ModeServe).Hello.Description },
		"call before ack":    func(e *Envelope) { *e = call(1) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := NewStream(ModeServe, testToken, Version{1, 0}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Accept(FromPlugin, hello(ModeServe)); err != nil {
				t.Fatal(err)
			}
			if _, ready := s.Negotiation(); ready {
				t.Fatal("ready before acknowledgment")
			}
			e := acknowledgment(s)
			mutate(&e)
			if err := s.Accept(FromHost, e); (err != nil) != (name != "valid") {
				t.Fatal(err)
			}
			if name == "valid" {
				var b bytes.Buffer
				if err := NewEncoder(&b).Encode(e); err != nil {
					t.Fatal(err)
				}
				if _, err := NewDecoder(&b).Decode(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStreamConfiguration(t *testing.T) {
	for _, tc := range []struct {
		mode    Mode
		token   string
		version Version
		caps    []Capability
	}{
		{"wrong", testToken, Version{1, 0}, nil},
		{ModeServe, "bad", Version{1, 0}, nil},
		{ModeServe, testToken, Version{}, nil},
		{ModeServe, testToken, Version{1, 0}, []Capability{{"", Version{1, 0}}}},
	} {
		if _, err := NewStream(tc.mode, tc.token, tc.version, tc.caps); !errors.Is(err, ErrInvalidMessage) {
			t.Fatal(err)
		}
	}
	s, err := NewStream(ModeServe, testToken, Version{2, 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready := s.Negotiation(); ready {
		t.Fatal("ready before hello")
	}
	if err := s.Accept(FromPlugin, hello(ModeServe)); !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatal(err)
	}
	s = readyStream(t)
	n, ready := s.Negotiation()
	if !ready || n.Protocol != (Version{1, 0}) || s.token != "" {
		t.Fatal("handshake not consumed")
	}
	n.Capabilities[0].Name = "mutated"
	if err := s.Accept(FromHost, call(1)); err != nil {
		t.Fatal("negotiation aliases internal state")
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func TestShortWrite(t *testing.T) {
	if err := NewEncoder(shortWriter{}).Encode(cancel(1)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}
