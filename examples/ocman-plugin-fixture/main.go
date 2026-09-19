// ocman-plugin-fixture is a deterministic protocol example, not an integration
// with an external service. It performs no network or filesystem operations.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	d := plugin.Description{
		ID: "org.ocman.fixture", Name: "Conformance fixture", Version: "1.0.0",
		Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeHub, MaxConcurrency: 4,
		Capabilities:    []plugin.Capability{{Name: "action", Version: plugin.Version{Major: 1}}, {Name: "fixture", Version: plugin.Version{Major: 1}}},
		RequestedGrants: []string{"context.session"},
		Actions: []plugin.ActionDescriptor{
			{ID: "notice", Label: "Notice", Placement: "global", Surfaces: []string{"command-palette"}},
			{ID: "session", Label: "Session", Placement: "session", RequiredGrants: []string{"context.session"}, Surfaces: []string{"command-palette"}},
		},
	}
	action := plugin.ActionHandler(d, func(_ context.Context, _ plugin.Call, in plugin.ActionInvocation) ([]plugin.ActionResult, error) {
		text := "Fixture ready"
		if in.ActionID == "session" {
			text = in.Context.SessionID
		}
		return []plugin.ActionResult{{Kind: "notice", Text: text}}, nil
	})
	handler := func(ctx context.Context, call plugin.Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability == "action" {
			return action(ctx, call, emit)
		}
		switch call.Method {
		case "stream":
			for _, data := range []string{`{"step":1}`, `{"step":2}`} {
				if err := emit(json.RawMessage(data)); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`{"done":true}`), nil
		case "wait":
			<-ctx.Done()
			return nil, ctx.Err()
		case "error":
			return nil, errors.New("private diagnostic must never reach stdout")
		default:
			return nil, plugin.Failure(plugin.ErrorNotFound)
		}
	}
	if err := plugin.Run(context.Background(), plugin.Mode(os.Args[1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler); err != nil {
		os.Exit(1)
	}
}
