package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/sdk/protocol"
)

func TestUIHandler(t *testing.T) {
	bundle := UIBundle{Expose: UIIndexExpose, Data: bytes.Repeat([]byte("a"), protocol.UIBundleChunkBytes+1)}
	d := Description{ID: "org.example.ui", Name: "UI", Version: "1", Protocol: Version{Major: 1}, MaxConcurrency: 1, Scope: ScopeOwner,
		Capabilities: []Capability{UICapability}, RequestedGrants: []string{UIAppGrant}, UI: NewUIDescriptor("^1.0.0", bundle)}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	var gotMethod string
	handler := UIHandler(d, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		gotMethod = method
		if method != "items.list" {
			return nil, Failure(ErrorNotFound)
		}
		return map[string]json.RawMessage{"echo": params}, nil
	}, bundle)
	call := Call{Capability: "ui", Version: UICapability.Version, Method: "call", Params: json.RawMessage(`{"method":"items.list","params":{"n":1}}`)}
	value, err := handler(t.Context(), call, nil)
	if err != nil || string(value) != `{"echo":{"n":1}}` || gotMethod != "items.list" {
		t.Fatalf("%s %v", value, err)
	}
	call.Params = json.RawMessage(`{"method":"missing"}`)
	if _, err := handler(t.Context(), call, nil); !errors.Is(err, Failure(ErrorNotFound)) {
		t.Fatal(err)
	}
	call.Params = json.RawMessage(`{"method":""}`)
	if _, err := handler(t.Context(), call, nil); !errors.Is(err, Failure(ErrorInvalidArgument)) {
		t.Fatal(err)
	}

	var assembled []byte
	emit := func(data json.RawMessage) error {
		part, err := protocol.DecodeUIBundleChunk(data)
		assembled = append(assembled, part...)
		return err
	}
	stream := Call{Capability: "ui", Version: UICapability.Version, Method: "bundle", Params: json.RawMessage(`{"expose":"index"}`)}
	if value, err := handler(t.Context(), stream, emit); err != nil || string(value) != `{}` || !bytes.Equal(assembled, bundle.Data) {
		t.Fatalf("bundle %s %v %d", value, err, len(assembled))
	}
	stream.Params = json.RawMessage(`{"expose":"settings"}`)
	if _, err := handler(t.Context(), stream, emit); !errors.Is(err, Failure(ErrorNotFound)) {
		t.Fatal(err)
	}
	stale := UIHandler(d, nil, UIBundle{Expose: UIIndexExpose, Data: []byte("changed")})
	stream.Params = json.RawMessage(`{"expose":"index"}`)
	if _, err := stale(t.Context(), stream, emit); !errors.Is(err, Failure(ErrorInternal)) {
		t.Fatal("unapproved bundle served", err)
	}
	for _, other := range []Call{{Capability: "pane", Method: "call"}, {Capability: "ui", Version: Version{Major: 2}, Method: "call"}, {Capability: "ui", Version: UICapability.Version, Method: "write"}} {
		if _, err := handler(t.Context(), other, emit); !errors.Is(err, Failure(ErrorNotFound)) {
			t.Fatalf("%+v %v", other, err)
		}
	}
	call.Params = json.RawMessage(`{"method":"x"}`)
	if _, err := stale(t.Context(), call, nil); !errors.Is(err, Failure(ErrorNotFound)) {
		t.Fatal("nil call func", err)
	}
}
