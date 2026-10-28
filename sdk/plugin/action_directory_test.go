package plugin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/sdk/protocol"
	"github.com/ocman/ocman/sdk/plugin"
)

func TestBrokerProjectDirectoryReachesSDK(t *testing.T) {
	d := description()
	d.RequestedGrants = []string{"context.project.path"}
	d.Actions[0].Placement = "project"
	d.Actions[0].RequiredGrants = d.RequestedGrants
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	var directory string
	h := plugin.ActionHandler(d, func(_ context.Context, _ plugin.Call, invocation plugin.ActionInvocation) ([]plugin.ActionResult, error) {
		directory = invocation.Context.Directory
		return []plugin.ActionResult{{Kind: "notice", Text: "OK"}}, nil
	})
	b := protocol.NewActionBroker(func(_ context.Context, _ string, use func(protocol.Description, []string) error) error {
		return use(d, d.RequestedGrants)
	}, func(ctx context.Context, _ string, call protocol.Call) (<-chan protocol.Reply, error) {
		value, err := h(ctx, call, func(json.RawMessage) error { return nil })
		replies := make(chan protocol.Reply, 1)
		result := &protocol.Result{Value: value}
		if err != nil {
			result.Error = protocol.RequestError(err)
			result.Value = nil
		}
		replies <- protocol.Reply{Message: protocol.Envelope{Type: protocol.TypeResult, Result: result}}
		return replies, nil
	})
	b.ResolveDirectory = func(context.Context, string) string { return "/srv/repo" }
	req := protocol.ActionRequest{PluginID: d.ID, ActionID: "test", OperationID: "op-1", Placement: "project", Surface: "command-palette", Context: protocol.ActionContext{ProjectID: "project-1"}}
	if result := b.Invoke(t.Context(), req); result.Error != nil || directory != "/srv/repo" {
		t.Fatalf("result=%+v directory=%q", result, directory)
	}
	req.OperationID, req.Context.Directory = "op-2", "/untrusted"
	if result := b.Invoke(t.Context(), req); result.Error == nil || result.Error.Category != protocol.ErrorInvalidArgument {
		t.Fatalf("caller directory accepted: %+v", result)
	}
}
