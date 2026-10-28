// ocman-plugin-catalog demonstrates in-app plugin UI (ui.v1): a routed React
// index built with @ocman/plugin-vite from ocman-native components, backed by
// plugin-defined calls and one confirmed action. Its data is synthetic.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/ocman/ocman/sdk/plugin"
)

func description(bundle plugin.UIBundle) plugin.Description {
	return plugin.Description{
		ID: "org.ocman.catalog", Name: "Catalog", Version: "1.0.0",
		Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeOwner, MaxConcurrency: 4,
		Capabilities:    []plugin.Capability{{Name: "action", Version: plugin.Version{Major: 1}}, plugin.UICapability},
		RequestedGrants: []string{plugin.UIAppGrant},
		Actions: []plugin.ActionDescriptor{{ID: "reset", Label: "Restore sample catalog", Placement: "global",
			Surfaces: []string{"command-palette"}, Confirmation: "Restore the sample catalog and discard status changes?"}},
		UI: plugin.NewUIDescriptor("^1.0.0", bundle),
	}
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	bundle := plugin.UIBundle{Expose: plugin.UIIndexExpose, Data: indexBundle()}
	d := description(bundle)
	catalog := NewCatalog()
	action := plugin.ActionHandler(d, func(context.Context, plugin.Call, plugin.ActionInvocation) ([]plugin.ActionResult, error) {
		catalog.Reset()
		return []plugin.ActionResult{{Kind: "notice", Text: "Sample catalog restored"}}, nil
	})
	ui := plugin.UIHandler(d, catalog.Call, bundle)
	handler := func(ctx context.Context, call plugin.Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		// Conformance-only methods: wait honors cancellation, error is normalized.
		switch call.Method {
		case "wait":
			<-ctx.Done()
			return nil, ctx.Err()
		case "error":
			return nil, plugin.Failure(plugin.ErrorUnavailable)
		}
		switch call.Capability {
		case "action":
			return action(ctx, call, emit)
		case plugin.UICapability.Name:
			return ui(ctx, call, emit)
		}
		return nil, plugin.Failure(plugin.ErrorNotFound)
	}
	if plugin.Run(context.Background(), plugin.Mode(os.Args[1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler) != nil {
		os.Exit(1)
	}
}
