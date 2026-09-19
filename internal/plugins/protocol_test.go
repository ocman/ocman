package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func hello(mode Mode) Envelope {
	return Envelope{Type: TypeHello, Hello: &Hello{Mode: mode, Token: testToken, Description: &Description{
		ID: "org.example.test", Name: "Test", Version: "1.0.0", Protocol: Version{1, 2},
		Capabilities:   []Capability{{Name: "action", Version: Version{1, 3}}},
		MaxConcurrency: 2, Scope: ScopeOwner,
	}}}
}

func call(id uint64) Envelope {
	return Envelope{Type: TypeCall, Call: &Call{ID: id, OperationID: "operation-1", Capability: "action", Version: Version{1, 0}, Method: "invoke", DeadlineUnixMS: 1800000000000, Params: json.RawMessage(`{}`)}}
}

func chunk(id, sequence uint64) Envelope {
	return Envelope{Type: TypeChunk, Chunk: &Chunk{ID: id, Sequence: sequence, Data: json.RawMessage(`"part"`)}}
}

func result(id uint64) Envelope {
	return Envelope{Type: TypeResult, Result: &Result{ID: id, Value: json.RawMessage(`null`)}}
}

func cancel(id uint64) Envelope {
	return Envelope{Type: TypeCancel, Cancel: &Cancel{ID: id}}
}

func TestCodecRoundTrip(t *testing.T) {
	for _, e := range []Envelope{
		hello(ModeDescribe), hello(ModeServe), call(1), chunk(1, 1), result(1),
		{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: ErrorCancelled}}},
		{Type: TypeEvent, Event: &Event{Capability: "action", Name: "changed", Data: json.RawMessage(`[]`)}},
		cancel(1), {Type: TypeShutdown, Shutdown: &Shutdown{}},
	} {
		t.Run(string(e.Type), func(t *testing.T) {
			var b bytes.Buffer
			if err := NewEncoder(&b).Encode(e); err != nil {
				t.Fatal(err)
			}
			d := NewDecoder(&b)
			got, err := d.Decode()
			if err != nil || !reflect.DeepEqual(got, e) {
				t.Fatalf("got %#v, %v; want %#v", got, err, e)
			}
			if _, err := d.Decode(); !errors.Is(err, io.EOF) {
				t.Fatalf("EOF: %v", err)
			}
		})
	}
}

func TestCodecInvalid(t *testing.T) {
	for name, input := range map[string]string{
		"blank": "\n", "log": "debug: hello\n", "array": "[]\n", "null": "null\n",
		"unknown type":     `{"type":"other"}` + "\n",
		"missing body":     `{"type":"cancel"}` + "\n",
		"wrong body":       `{"type":"cancel","shutdown":{}}` + "\n",
		"multiple bodies":  `{"type":"cancel","cancel":{"id":"1"},"shutdown":{}}` + "\n",
		"null body":        `{"type":"cancel","cancel":null}` + "\n",
		"zero id":          `{"type":"cancel","cancel":{"id":"0"}}` + "\n",
		"numeric id":       `{"type":"cancel","cancel":{"id":1}}` + "\n",
		"duplicate":        `{"type":"shutdown","type":"shutdown","shutdown":{}}` + "\n",
		"nested duplicate": `{"type":"cancel","cancel":{"id":"1","id":"2"}}` + "\n",
		"case alias":       `{"type":"cancel","cancel":{"ID":"1"}}` + "\n",
		"two values":       `{"type":"shutdown","shutdown":{}} {}` + "\n",
		"unterminated":     `{"type":"shutdown","shutdown":{}}`,
		"multiline":        "{\n\"type\":\"shutdown\",\"shutdown\":{}}\n",
		"invalid utf8":     "{\"type\":\"shutdown\",\"shutdown\":{},\"extra\":\"\xff\"}\n",
		"deep":             `{"type":"shutdown","shutdown":{},"extra":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := NewDecoder(strings.NewReader(input))
			if _, err := d.Decode(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("got %v", err)
			}
			if _, err := d.Decode(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("decoder must remain failed: %v", err)
			}
		})
	}
}

func TestAdditiveFields(t *testing.T) {
	input := `{"type":"cancel","future":{"x":1},"cancel":{"id":"1","reason":"user"}}` + "\r\n"
	got, err := NewDecoder(strings.NewReader(input)).Decode()
	if err != nil || !reflect.DeepEqual(got, cancel(1)) {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestMessageLimit(t *testing.T) {
	prefix := `{"type":"shutdown","shutdown":{},"padding":"`
	for _, delta := range []int{-1, 0, 1} {
		line := prefix + strings.Repeat("x", MaxMessageBytes-len(prefix)-2+delta) + `"}`
		_, err := NewDecoder(strings.NewReader(line + "\n")).Decode()
		if delta <= 0 && err != nil {
			t.Fatalf("size %d: %v", len(line), err)
		}
		if delta > 0 && !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("size %d: %v", len(line), err)
		}
	}
	e := result(1)
	e.Result.Value = json.RawMessage(`"` + strings.Repeat("x", MaxMessageBytes) + `"`)
	var b bytes.Buffer
	if err := NewEncoder(&b).Encode(e); !errors.Is(err, ErrMessageTooLarge) || b.Len() != 0 {
		t.Fatalf("oversize write: %v, %d", err, b.Len())
	}
}

func TestNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		process Version
		caps    []Capability
		want    Version
		count   int
		bad     bool
	}{
		{"compatible", Version{1, 1}, []Capability{{"action", Version{1, 2}}}, Version{1, 1}, 1, false},
		{"newer host", Version{1, 5}, []Capability{{"action", Version{1, 5}}}, Version{1, 2}, 1, false},
		{"process major", Version{2, 0}, nil, Version{}, 0, true},
		{"invalid version", Version{}, nil, Version{}, 0, true},
		{"capability major", Version{1, 0}, []Capability{{"action", Version{2, 0}}}, Version{1, 0}, 0, false},
		{"unknown capability", Version{1, 0}, []Capability{{"future", Version{1, 0}}}, Version{1, 0}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Negotiate(tc.process, tc.caps, *hello(ModeServe).Hello.Description)
			if (err != nil) != tc.bad {
				t.Fatalf("error: %v", err)
			}
			if !tc.bad && (got.Protocol != tc.want || len(got.Capabilities) != tc.count) {
				t.Fatalf("got %#v", got)
			}
			if tc.count > 0 && got.Capabilities[0].Version.Minor != min(tc.caps[0].Version.Minor, 3) {
				t.Fatal("capability minor not negotiated independently")
			}
		})
	}
}

func TestStream(t *testing.T) {
	type step struct {
		direction Direction
		envelope  Envelope
	}
	h := func(e Envelope) step { return step{FromHost, e} }
	p := func(e Envelope) step { return step{FromPlugin, e} }
	for _, tc := range []struct {
		name  string
		steps []step
		bad   bool
	}{
		{"result", []step{h(call(1)), p(result(1))}, false},
		{"interleaved", []step{h(call(1)), h(call(2)), p(chunk(2, 1)), p(chunk(1, 1)), p(chunk(2, 2)), p(result(1)), p(result(2))}, false},
		{"cancel race", []step{h(call(1)), h(cancel(1)), p(chunk(1, 1)), p(result(1))}, false},
		{"cancelled", []step{h(call(1)), h(cancel(1)), p(Envelope{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: ErrorCancelled}}})}, false},
		{"duplicate cancel", []step{h(call(1)), h(cancel(1)), h(cancel(1))}, false},
		{"unknown result", []step{p(result(1))}, true},
		{"unknown cancel", []step{h(cancel(1))}, true},
		{"duplicate result", []step{h(call(1)), p(result(1)), p(result(1))}, true},
		{"chunk after result", []step{h(call(1)), p(result(1)), p(chunk(1, 1))}, true},
		{"skip chunk", []step{h(call(1)), p(chunk(1, 2))}, true},
		{"duplicate chunk", []step{h(call(1)), p(chunk(1, 1)), p(chunk(1, 1))}, true},
		{"reuse id", []step{h(call(1)), p(result(1)), h(call(1))}, true},
		{"concurrency", []step{h(call(1)), h(call(2)), h(call(3))}, true},
		{"wrong direction", []step{p(call(1))}, true},
		{"repeat hello", []step{p(hello(ModeServe))}, true},
		{"shutdown", []step{h(call(1)), h(Envelope{Type: TypeShutdown, Shutdown: &Shutdown{}})}, false},
		{"after shutdown", []step{h(Envelope{Type: TypeShutdown, Shutdown: &Shutdown{}}), h(call(1))}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			for i, step := range tc.steps {
				err = s.Accept(step.direction, step.envelope)
				if i < len(tc.steps)-1 && err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			if (err != nil) != tc.bad {
				t.Fatalf("got %v, want failure %v", err, tc.bad)
			}
		})
	}
}

func TestHandshake(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      Mode
		token     string
		first     Envelope
		direction Direction
		bad       bool
	}{
		{"serve", ModeServe, testToken, hello(ModeServe), FromPlugin, false},
		{"describe", ModeDescribe, testToken, hello(ModeDescribe), FromPlugin, false},
		{"wrong token", ModeServe, strings.Repeat("a", 64), hello(ModeServe), FromPlugin, true},
		{"wrong mode", ModeDescribe, testToken, hello(ModeServe), FromPlugin, true},
		{"call first", ModeServe, testToken, call(1), FromHost, true},
		{"host hello", ModeServe, testToken, hello(ModeServe), FromHost, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewStream(tc.mode, tc.token, Version{1, 0}, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = s.Accept(tc.direction, tc.first)
			if (err != nil) != tc.bad {
				t.Fatalf("got %v", err)
			}
			if tc.bad || tc.mode == ModeDescribe {
				if err := s.Accept(FromPlugin, hello(tc.mode)); err == nil {
					t.Fatal("accepted another handshake")
				}
			}
		})
	}
}

type brokenIO struct{}

func (brokenIO) Read([]byte) (int, error)  { return 0, io.ErrClosedPipe }
func (brokenIO) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCodecIOErrors(t *testing.T) {
	if _, err := NewDecoder(brokenIO{}).Decode(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	e := NewEncoder(brokenIO{})
	for range 2 {
		if err := e.Encode(cancel(1)); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
}
