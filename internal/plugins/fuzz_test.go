package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// FuzzDecoder feeds arbitrary NDJSON to the plugin protocol decoder. Every
// envelope it accepts must be valid, re-encodable, and decode back to the
// same envelope: the host relays what it accepted, so an accepted frame the
// encoder rejects or changes would break the conversation mid-stream.
func FuzzDecoder(f *testing.F) {
	for _, e := range []Envelope{
		hello(ModeServe), hello(ModeDescribe),
		{Type: TypeHello, Hello: &Hello{Mode: ModeServe, Accepted: &Negotiated{Protocol: Version{1, 0}, Capabilities: []Capability{{"action", Version{1, 0}}}}}},
		{Type: TypeCall, Call: &Call{ID: 1, OperationID: "op-1", Capability: "action", Version: Version{1, 0}, Method: "invoke", DeadlineUnixMS: 1, Params: json.RawMessage(`{"a":[1,2,{"b":null}]}`)}},
		{Type: TypeChunk, Chunk: &Chunk{ID: 1, Sequence: 1, Data: json.RawMessage(`"x"`)}},
		{Type: TypeResult, Result: &Result{ID: 1, Value: json.RawMessage(`{"results":[]}`)}},
		{Type: TypeResult, Result: &Result{ID: 1, Error: &WireError{Category: ErrorInternal}}},
		{Type: TypeEvent, Event: &Event{Capability: "conversation", Name: "message", Data: json.RawMessage(`{"accountId":"T1","threadId":"C1/1","eventId":"E1","text":"hi"}`)}},
		{Type: TypeCancel, Cancel: &Cancel{ID: 3}},
		{Type: TypeShutdown, Shutdown: &Shutdown{}},
	} {
		line, err := json.Marshal(e)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(append(line, '\n'))
	}
	f.Add([]byte("{\"type\":\"shutdown\",\"shutdown\":{}}\r\n{\"type\":\"cancel\",\"cancel\":{\"id\":\"1\"}}\n"))
	f.Add([]byte("{\"type\":\"chunk\",\"chunk\":{\"id\":1,\"sequence\":1,\"data\":\"<\\u2028>\"},\"extra\":1}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		d := NewDecoder(bytes.NewReader(data))
		for range 8 {
			e, err := d.Decode()
			if err != nil {
				return
			}
			if err := e.Validate(); err != nil {
				t.Fatalf("decoded envelope fails Validate: %v", err)
			}
			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(e); err != nil {
				// HTML escaping can grow a frame past the size limit.
				if errors.Is(err, ErrMessageTooLarge) {
					continue
				}
				t.Fatalf("decoded envelope cannot be re-encoded: %v (%+v)", err, e)
			}
			again, err := NewDecoder(bytes.NewReader(out.Bytes())).Decode()
			if err != nil {
				t.Fatalf("re-encoded envelope rejected: %v\n%s", err, out.Bytes())
			}
			if !sameJSON(normalizeEnvelope(t, e), normalizeEnvelope(t, again)) {
				t.Fatalf("round trip changed the envelope\n%#v\n%#v", e, again)
			}
		}
	})
}

// FuzzDecodePaneTree checks pane trees a plugin returns stay bounded and
// acyclic once accepted (the sidebar renders them recursively), and survive
// a re-encode unchanged.
func FuzzDecodePaneTree(f *testing.F) {
	f.Add([]byte(`{"available":true,"warning":true,"nodes":[{"id":"a","title":"<script>","status":"open","badge":"P1","kind":"task"},{"id":"b","title":"Child","parentId":"a"}]}`))
	f.Add([]byte(`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"missing"}]}`))
	f.Add([]byte(`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"b"},{"id":"b","title":"B","parentId":"a"}]}`))
	f.Add([]byte(`{"available":true,"nodes":[{"id":"a","title":"A","parentId":"a"}]}`))
	f.Add([]byte(`{"available":false}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		tree, err := DecodePaneTree(data)
		if err != nil {
			return
		}
		if len(tree.Nodes) > 2000 {
			t.Fatalf("%d nodes accepted", len(tree.Nodes))
		}
		parent := map[string]string{}
		for _, n := range tree.Nodes {
			if n.ID == "" {
				t.Fatal("node without id accepted")
			}
			if _, dup := parent[n.ID]; dup {
				t.Fatalf("duplicate id %q accepted", n.ID)
			}
			parent[n.ID] = n.ParentID
		}
		for id := range parent {
			depth := 0
			for cur := id; cur != ""; cur = parent[cur] {
				if depth++; depth > 65 {
					t.Fatalf("cycle or depth over 64 through %q accepted", id)
				}
			}
		}
		b, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodePaneTree(b)
		if err != nil {
			t.Fatalf("re-encoded tree rejected: %v\n%s", err, b)
		}
		if !sameJSON(tree, again) {
			t.Fatalf("round trip changed the tree\n%s", b)
		}
	})
}

// normalizeEnvelope compacts the raw JSON fields an envelope carries, which
// the encoder is free to re-space.
func normalizeEnvelope(t *testing.T, e Envelope) Envelope {
	compact := func(r json.RawMessage) json.RawMessage {
		if r == nil {
			return nil
		}
		var b bytes.Buffer
		if err := json.Compact(&b, r); err != nil {
			t.Fatalf("accepted raw JSON does not compact: %v", err)
		}
		return b.Bytes()
	}
	if e.Call != nil {
		c := *e.Call
		c.Params = compact(c.Params)
		e.Call = &c
	}
	if e.Chunk != nil {
		c := *e.Chunk
		c.Data = compact(c.Data)
		e.Chunk = &c
	}
	if e.Result != nil {
		c := *e.Result
		c.Value = compact(c.Value)
		e.Result = &c
	}
	if e.Event != nil {
		c := *e.Event
		c.Data = compact(c.Data)
		e.Event = &c
	}
	if e.Hello != nil && e.Hello.Description != nil {
		h := *e.Hello
		d := *h.Description
		d.Settings = append([]Setting(nil), d.Settings...)
		for i := range d.Settings {
			d.Settings[i].Default = compact(d.Settings[i].Default)
		}
		h.Description = &d
		e.Hello = &h
	}
	return e
}

func sameJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
