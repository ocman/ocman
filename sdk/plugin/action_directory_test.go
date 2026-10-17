package plugin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/sdk/plugin"
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
	b := plugins.NewActionBroker(func(_ context.Context, _ string, use func(plugins.Description, []string) error) error {
		return use(d, d.RequestedGrants)
	}, func(ctx context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
		value, err := h(ctx, call, func(json.RawMessage) error { return nil })
		replies := make(chan plugins.Reply, 1)
		result := &plugins.Result{Value: value}
		if err != nil {
			result.Error = plugins.RequestError(err)
			result.Value = nil
		}
		replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: result}}
		return replies, nil
	})
	b.ResolveDirectory = func(context.Context, string) string { return "/srv/repo" }
	req := plugins.ActionRequest{PluginID: d.ID, ActionID: "test", OperationID: "op-1", Placement: "project", Surface: "command-palette", Context: plugins.ActionContext{ProjectID: "project-1"}}
	if result := b.Invoke(t.Context(), req); result.Error != nil || directory != "/srv/repo" {
		t.Fatalf("result=%+v directory=%q", result, directory)
	}
	req.OperationID, req.Context.Directory = "op-2", "/untrusted"
	if result := b.Invoke(t.Context(), req); result.Error == nil || result.Error.Category != plugins.ErrorInvalidArgument {
		t.Fatalf("caller directory accepted: %+v", result)
	}
}
