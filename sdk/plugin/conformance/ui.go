package conformance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/sdk/plugin"
	"github.com/ocman/ocman/sdk/protocol"
)

// RunUI checks a ui.v1 plugin: an owner-scoped declaration requesting
// ui.app with a compatible SDK range, every declared bundle streamed in
// bounded chunks matching its approved checksum, a unary plugin-defined call,
// and normalized errors for unknown methods, invalid params and undeclared
// exposes. call must be a successful, side-effect-free method.
func RunUI(t *testing.T, executable string, call plugin.UICall) {
	t.Helper()
	c, d := start(t, executable, plugin.ModeServe, strings.Repeat("9a", 32))
	if d.UI == nil || d.Scope != plugin.ScopeOwner || !slices.Contains(d.RequestedGrants, plugin.UIAppGrant) {
		t.Fatal("ui plugins must declare an owner-scoped UI and request ui.app")
	}
	if _, ok := d.UI.Exposes[plugin.UIIndexExpose]; !ok {
		t.Fatal("ui plugins must declare the index expose")
	}
	if !protocol.UISDKCompatible(d.UI.SDK, plugin.UISDKVersion) {
		t.Fatalf("SDK range %q is incompatible with host %s", d.UI.SDK, plugin.UISDKVersion)
	}
	c.ready()
	invoke := func(method string, params any) (plugin.Result, []json.RawMessage) {
		t.Helper()
		raw, _ := json.Marshal(params)
		id := c.call(plugin.Call{Capability: plugin.UICapability.Name, Version: plugin.UICapability.Version, Method: method, Params: raw}, time.Now().Add(5*time.Second))
		var chunks []json.RawMessage
		for {
			e, err := c.decoder.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if err := c.stream.Accept(plugin.FromPlugin, e); err != nil {
				t.Fatal(err)
			}
			if e.Chunk != nil && e.Chunk.ID == id {
				chunks = append(chunks, e.Chunk.Data)
			}
			if e.Result != nil && e.Result.ID == id {
				return *e.Result, chunks
			}
		}
	}
	for expose, declared := range d.UI.Exposes {
		r, chunks := invoke("bundle", plugin.UIBundleRequest{Expose: expose})
		if r.Error != nil || len(chunks) == 0 {
			t.Fatalf("bundle %s: %+v", expose, r)
		}
		var bundle []byte
		for _, chunk := range chunks {
			part, err := protocol.DecodeUIBundleChunk(chunk)
			if err != nil {
				t.Fatalf("bundle %s chunk: %v", expose, err)
			}
			bundle = append(bundle, part...)
		}
		if protocol.VerifyUIBundle(declared, bundle) != nil {
			t.Fatalf("bundle %s does not match its approved checksum", expose)
		}
	}
	r, chunks := invoke("call", call)
	if r.Error != nil || len(chunks) != 0 || !json.Valid(r.Value) {
		t.Fatalf("ui call must succeed without chunks: %+v", r)
	}
	for _, tc := range []struct {
		method   string
		params   any
		category plugin.ErrorCategory
	}{
		{"call", plugin.UICall{Method: "conformance.unknown-method"}, plugin.ErrorNotFound},
		{"call", map[string]string{"method": ""}, plugin.ErrorInvalidArgument},
		{"bundle", plugin.UIBundleRequest{Expose: "conformance-undeclared"}, plugin.ErrorNotFound},
	} {
		r, _ := invoke(tc.method, tc.params)
		if r.Error == nil || r.Error.Category != tc.category {
			t.Fatalf("%s %+v: want %s, got %+v", tc.method, tc.params, tc.category, r)
		}
	}
	c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
	c.exit()
}
