package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/sdk/plugin"
)

// RunActionGrants exercises a grant-requiring action through the canonical host
// broker. Invocation must be a successful, side-effect-free action with nonempty
// required grants. It checks denied admission, granted dispatch, context
// minimization, deduplication and denial of cached results after revocation.
func RunActionGrants(t *testing.T, executable string, invocation plugin.ActionInvocation) {
	t.Helper()
	c, d := start(t, executable, plugin.ModeServe, "abababababababababababababababababababababababababababababababab")
	c.ready()
	var action *plugin.ActionDescriptor
	for i := range d.Actions {
		if d.Actions[i].ID == invocation.ActionID {
			action = &d.Actions[i]
			break
		}
	}
	if action == nil || len(action.RequiredGrants) == 0 {
		t.Fatal("grant test requires a declared action with required grants")
	}
	var grants []string
	calls := 0
	broker := plugins.NewActionBroker(func(_ context.Context, _ string, admit func(plugins.Description, []string) error) error {
		return admit(d, grants)
	}, func(_ context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
		calls++
		// The broker must strip every context field not required by this action.
		var sent plugin.ActionInvocation
		if err := json.Unmarshal(call.Params, &sent); err != nil {
			t.Fatal(err)
		}
		allowed := map[string]bool{}
		for _, g := range action.RequiredGrants {
			allowed[g] = true
		}
		if (!allowed["context.owner"] && sent.Context.OwnerID != "") ||
			(!allowed["context.project"] && sent.Context.ProjectID != "") ||
			(!allowed["context.session"] && sent.Context.SessionID != "") ||
			(!allowed["context.route"] && sent.Context.Route != "") ||
			(!allowed["context.selection"] && len(sent.Context.Selection) != 0) {
			t.Fatal("host disclosed undeclared context")
		}
		id := c.call(call, time.Now().Add(time.Second))
		result, chunks := c.result(id)
		if chunks != 0 {
			t.Fatal("action emitted chunks")
		}
		replies := make(chan plugins.Reply, 1)
		replies <- plugins.Reply{Message: plugin.Envelope{Type: plugin.TypeResult, Result: &result}}
		close(replies)
		return replies, nil
	})
	r := plugins.ActionRequest{PluginID: d.ID, ActionID: action.ID, OperationID: "grant-test", Placement: action.Placement, Surface: "command-palette", Context: invocation.Context}
	response := broker.Invoke(t.Context(), r)
	if response.Error == nil || response.Error.Category != plugin.ErrorPermissionDenied || calls != 0 {
		t.Fatalf("denied action dispatched: %+v", response)
	}
	grants = action.RequiredGrants
	response = broker.Invoke(t.Context(), r)
	if response.Confirmation != nil {
		r.ConfirmationToken = response.Confirmation.Token
		response = broker.Invoke(t.Context(), r)
	}
	if response.Error != nil || len(response.Results) == 0 || calls != 1 {
		t.Fatalf("granted action failed: %+v", response)
	}
	response = broker.Invoke(t.Context(), r)
	if response.Error != nil || calls != 1 {
		t.Fatalf("duplicate action dispatched: %+v", response)
	}
	grants = nil
	response = broker.Invoke(t.Context(), r)
	if response.Error == nil || response.Error.Category != plugin.ErrorPermissionDenied || calls != 1 {
		t.Fatalf("revoked cached result exposed: %+v", response)
	}
	c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
	c.exit()
}
