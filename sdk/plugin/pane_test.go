package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPaneHandler(t *testing.T) {
	d := Description{Scope: ScopeOwner, Capabilities: []Capability{PaneCapability}, RequestedGrants: []string{PaneProjectGrant}, Panes: []PaneDescriptor{{ID: "items", Label: "Items"}}}
	called := 0
	handler := PaneHandler(d, func(ctx context.Context, r PaneRead) (PaneTree, error) {
		called++
		if r.Directory != "/repo" || r.PaneID != "items" {
			t.Fatal(r)
		}
		if err := ctx.Err(); err != nil {
			return PaneTree{}, err
		}
		return PaneTree{Available: true, Nodes: []TreeNode{{ID: "a", Title: "A"}}}, nil
	})
	call := Call{Capability: "pane", Version: PaneCapability.Version, Method: "read", Params: json.RawMessage(`{"paneId":"items","directory":"/repo"}`)}
	data, err := handler(t.Context(), call, nil)
	if err != nil || !json.Valid(data) || called != 1 {
		t.Fatalf("%s %v calls=%d", data, err, called)
	}
	for _, change := range []func(*Call){func(c *Call) { c.Capability = "other" }, func(c *Call) { c.Version.Major = 2 }, func(c *Call) { c.Method = "write" }, func(c *Call) { c.Params = json.RawMessage(`{}`) }, func(c *Call) { c.Params = json.RawMessage(`{"paneId":"missing","directory":"/repo"}`) }} {
		next := call
		change(&next)
		if _, err := handler(t.Context(), next, nil); err == nil {
			t.Fatalf("accepted %+v", next)
		}
	}
	if called != 1 {
		t.Fatal("invalid requests invoked handler")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := handler(ctx, call, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	invalid := PaneHandler(d, func(context.Context, PaneRead) (PaneTree, error) {
		return PaneTree{Available: true, Nodes: []TreeNode{{ID: "a"}}}, nil
	})
	if _, err := invalid(t.Context(), call, nil); err == nil {
		t.Fatal("invalid result accepted")
	}
}
