package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ocman/ocman/sdk/protocol"
)

func TestAssetHandler(t *testing.T) {
	data := []byte(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="8"/></svg>`)
	d := Description{Icon: &IconDescriptor{Path: "assets/wrench.svg"}}
	files := fstest.MapFS{"assets/wrench.svg": &fstest.MapFile{Data: data}}
	h := AssetHandler(d, files)
	c := Call{Capability: "asset", Version: Version{Major: 1}, Method: "read", Params: json.RawMessage(`{"path":"assets/wrench.svg"}`)}
	raw, err := h(t.Context(), c, nil)
	got, decodeErr := protocol.DecodeAssetResult(raw)
	if err != nil || decodeErr != nil || string(got) != string(data) {
		t.Fatalf("asset: %s %v %v", raw, err, decodeErr)
	}
	for _, tc := range []struct {
		name string
		call Call
		want ErrorCategory
	}{
		{"capability", Call{Capability: "ui"}, ErrorNotFound},
		{"version", Call{Capability: "asset", Version: Version{Major: 2}}, ErrorNotFound},
		{"method", Call{Capability: "asset", Version: Version{Major: 1}, Method: "write"}, ErrorNotFound},
		{"undeclared", Call{Capability: "asset", Version: Version{Major: 1}, Method: "read", Params: json.RawMessage(`{"path":"other.svg"}`)}, ErrorNotFound},
		{"traversal", Call{Capability: "asset", Version: Version{Major: 1}, Method: "read", Params: json.RawMessage(`{"path":"../secret.svg"}`)}, ErrorInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h(t.Context(), tc.call, nil)
			if !errors.Is(err, Failure(tc.want)) {
				t.Fatalf("want %s: %v", tc.want, err)
			}
		})
	}
	for _, handler := range []Handler{AssetHandler(Description{}, files), AssetHandler(d, nil), AssetHandler(d, fstest.MapFS{})} {
		if _, err := handler(t.Context(), c, nil); !errors.Is(err, Failure(ErrorNotFound)) {
			t.Fatalf("missing: %v", err)
		}
	}
	for _, bad := range []string{`<svg><script/></svg>`, strings.Repeat("x", MaxIconBytes+1)} {
		handler := AssetHandler(d, fstest.MapFS{"assets/wrench.svg": &fstest.MapFile{Data: []byte(bad)}})
		if _, err := handler(t.Context(), c, nil); !errors.Is(err, Failure(ErrorInvalidArgument)) {
			t.Fatalf("invalid: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h(ctx, c, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
