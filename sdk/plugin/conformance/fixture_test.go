package conformance_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/sdk/plugin"
	"github.com/NoUseFreak/ocman/sdk/plugin/conformance"
)

func TestFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ocman-plugin-fixture")
	cmd := exec.Command("go", "build", "-o", path, "../../../examples/ocman-plugin-fixture")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	action := plugin.Call{Capability: "action", Version: plugin.Version{Major: 1}, Method: "invoke", Params: json.RawMessage(`{"actionId":"notice","context":{}}`)}
	fixture := func(method string) plugin.Call {
		return plugin.Call{Capability: "fixture", Version: plugin.Version{Major: 1}, Method: method, Params: json.RawMessage(`{}`)}
	}
	stream := fixture("stream")
	conformance.Run(t, path, conformance.Cases{Success: action, Wait: fixture("wait"), Error: fixture("error"), ErrorCategory: plugin.ErrorInternal, Stream: &stream})
	t.Run("reusable-action-grants", func(t *testing.T) {
		conformance.RunActionGrants(t, path, plugin.ActionInvocation{ActionID: "session", Context: plugin.ActionContext{SessionID: "ses-1", OwnerID: "local"}})
	})
	t.Run("reusable-conversation-grants", func(t *testing.T) {
		conformance.RunConversationGrants(t, path, "/conformance/project")
	})
	t.Run("host-discovery-process-grants", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entries, err := plugins.Scan(ctx, filepath.Dir(path), nil)
		if err != nil || len(entries) != 1 || entries[0].Err != nil {
			t.Fatalf("discover: %+v %v", entries, err)
		}
		dataDir := t.TempDir()
		if err := os.Chmod(dataDir, 0700); err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{}, 1)
		p, err := plugins.StartProcess(ctx, plugins.LaunchConfig{Candidate: entries[0], DataDir: dataDir, Supported: entries[0].Description.Capabilities, OnHealth: func(h plugins.Health) {
			if h.Status == "ready" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Stop()
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		var grants []string
		calls := 0
		broker := plugins.NewActionBroker(func(_ context.Context, _ string, admit func(plugins.Description, []string) error) error {
			return admit(entries[0].Description, grants)
		}, func(ctx context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
			calls++
			return p.Call(ctx, call)
		})
		r := plugins.ActionRequest{PluginID: entries[0].Description.ID, ActionID: "session", OperationID: "granted", Placement: "session", Surface: "command-palette", Context: plugins.ActionContext{SessionID: "ses-1", OwnerID: "local"}}
		response := broker.Invoke(ctx, r)
		if response.Error == nil || response.Error.Category != plugins.ErrorPermissionDenied || calls != 0 {
			t.Fatalf("missing grants dispatched: %+v", response)
		}
		grants = []string{"context.session"}
		response = broker.Invoke(ctx, r)
		if response.Error != nil || len(response.Results) != 1 || response.Results[0].Text != "ses-1" || calls != 1 {
			t.Fatalf("grant/minimization: %+v", response)
		}
		grants = nil
		response = broker.Invoke(ctx, r)
		if response.Error == nil || response.Error.Category != plugins.ErrorPermissionDenied || calls != 1 {
			t.Fatalf("revoked cached result: %+v", response)
		}
	})
}
