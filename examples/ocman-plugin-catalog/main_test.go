package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
	"github.com/ocman/ocman/sdk/plugin"
	"github.com/ocman/ocman/sdk/plugin/conformance"
)

func TestConformance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ocman-plugin-catalog")
	if out, err := exec.Command("go", "build", "-o", path, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	testutil.WarmExecutable(t, path)
	call := func(method string) plugin.Call {
		return plugin.Call{Capability: "action", Version: plugin.Version{Major: 1}, Method: method, Params: json.RawMessage(`{"actionId":"reset","context":{}}`)}
	}
	conformance.Run(t, path, conformance.Cases{Success: call("invoke"), Wait: call("wait"), Error: call("error"), ErrorCategory: plugin.ErrorUnavailable})
	t.Run("ui", func(t *testing.T) {
		conformance.RunUI(t, path, plugin.UICall{Method: "items.list", Params: json.RawMessage(`{"query":"lamp"}`)})
	})
}

func TestDescriptionDeclaresIndexBundle(t *testing.T) {
	bundle := plugin.UIBundle{Expose: plugin.UIIndexExpose, Data: indexBundle()}
	d := description(bundle)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.UI.Exposes[plugin.UIIndexExpose].Size != len(bundle.Data) {
		t.Fatal("index bundle not declared")
	}
}

func TestCatalogMethods(t *testing.T) {
	c := NewCatalog()
	ctx := context.Background()
	list := func(params string) []Item {
		t.Helper()
		v, err := c.Call(ctx, "items.list", json.RawMessage(params))
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)["items"].([]Item)
	}
	if got := list(`{}`); len(got) != 4 {
		t.Fatalf("list: %d", len(got))
	}
	if got := list(`{"query":"FACILITIES"}`); len(got) != 2 {
		t.Fatalf("filter: %+v", got)
	}
	item, err := c.Call(ctx, "items.get", json.RawMessage(`{"id":"router"}`))
	if err != nil || item.(Item).Name != "Office router" {
		t.Fatal(item, err)
	}
	updated, err := c.Call(ctx, "items.setStatus", json.RawMessage(`{"id":"router","status":"retired"}`))
	if err != nil || updated.(Item).Status != "retired" {
		t.Fatal(updated, err)
	}
	c.Reset()
	if item, _ = c.Call(ctx, "items.get", json.RawMessage(`{"id":"router"}`)); item.(Item).Status != "active" {
		t.Fatal("reset kept changes")
	}
	for _, tc := range []struct {
		method, params string
		category       plugin.ErrorCategory
	}{
		{"items.get", `{"id":"missing"}`, plugin.ErrorNotFound},
		{"items.setStatus", `{"id":"missing","status":"active"}`, plugin.ErrorNotFound},
		{"items.setStatus", `{"id":"lamp","status":"broken"}`, plugin.ErrorInvalidArgument},
		{"items.list", `[`, plugin.ErrorInvalidArgument},
		{"items.delete", `{}`, plugin.ErrorNotFound},
	} {
		if _, err := c.Call(ctx, tc.method, json.RawMessage(tc.params)); !errors.Is(err, plugin.Failure(tc.category)) {
			t.Errorf("%s %s: %v", tc.method, tc.params, err)
		}
	}
}
