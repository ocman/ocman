package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/sdk/plugin"
)

// RunSessionGrants exercises session.v1 through the canonical host broker. The
// executable must declare the capability and serve completed calls without side
// effects under a test configuration. It checks the declaration contract, denied
// and granted run admission (including subpath containment and closed params),
// and that a completed turn reaches the plugin as {sessionId, text} and is
// acknowledged with an empty object.
func RunSessionGrants(t *testing.T, executable string, project string) {
	t.Helper()
	c, d := start(t, executable, plugin.ModeServe, "abababababababababababababababababababababababababababababababab")
	c.ready()
	if d.Validate() != nil {
		t.Fatal("description does not satisfy the session declaration contract")
	}
	if d.Scope != plugin.ScopeOwner {
		t.Fatal("a session connector must belong to its owner machine")
	}
	var grants []string
	started, calls := []string{}, []plugins.Call{}
	broker := plugins.NewSessionBroker(
		func(_ context.Context, _ string, admit func(plugins.Description, []string, plugins.SessionConfig) error) error {
			return admit(d, grants, plugins.SessionConfig{Project: project})
		},
		func(_ context.Context, _ string, config plugins.SessionConfig, params plugins.SessionRunParams, directory, operationID string) (string, error) {
			if operationID == "" {
				t.Fatal("run reached the host without an operation id")
			}
			started = append(started, config.Project+"|"+directory+"|"+params.Prompt+"|"+params.Agent+"|"+params.Model+"|"+params.PermissionProfile)
			return "ses-conformance-1", nil
		},
		func(_ context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
			calls = append(calls, call)
			if call.Capability != plugins.SessionCapability.Name || call.Method != plugins.SessionCompletedMethod {
				t.Fatalf("unexpected call: %s/%s", call.Capability, call.Method)
			}
			id := c.call(call, time.Now().Add(time.Second))
			result, chunks := c.result(id)
			if chunks != 0 {
				t.Fatal("completed emitted chunks")
			}
			if result.Error != nil || string(result.Value) != `{}` {
				t.Fatalf("plugin must acknowledge a completion with {}: %+v", result)
			}
			replies := make(chan plugins.Reply, 1)
			replies <- plugins.Reply{Message: plugin.Envelope{Type: plugin.TypeResult, Result: &result}}
			close(replies)
			return replies, nil
		})

	runEvent := func(t *testing.T, params plugins.SessionRunParams) plugins.Call {
		t.Helper()
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		return plugins.Call{OperationID: "conformance-run", Capability: plugins.SessionCapability.Name, Version: plugins.SessionCapability.Version, Method: plugins.SessionRunMethod, Params: data}
	}
	params := plugins.SessionRunParams{Prompt: "conformance prompt", Agent: "build", Model: "provider/model", PermissionProfile: "read-only"}
	completion := plugins.SessionCompleted{SessionID: "ses-conformance-1", Text: "conformance completion"}

	if _, err := broker.Run(t.Context(), d.ID, runEvent(t, params)); err == nil || len(started) != 0 {
		t.Fatalf("ungranted run started a session: %v", err)
	}
	if err := broker.Completed(t.Context(), d.ID, "conformance-completed", completion); err == nil || len(calls) != 0 {
		t.Fatalf("ungranted completed dispatched: %v", err)
	}
	grants = []string{plugins.SessionRunGrant}
	result, err := broker.Run(t.Context(), d.ID, runEvent(t, params))
	if err != nil || result.SessionID != "ses-conformance-1" {
		t.Fatalf("granted run failed: %+v %v", result, err)
	}
	// The directory is always the approved project, never a caller-supplied path.
	if len(started) != 1 || started[0] != project+"|"+project+"|conformance prompt|build|provider/model|read-only" {
		t.Fatalf("run did not start in the approved project: %v", started)
	}
	for _, bad := range []string{"../escape", "/etc", "a/../../b", ".."} {
		traversal := params
		traversal.Subpath = bad
		if _, err := broker.Run(t.Context(), d.ID, runEvent(t, traversal)); err == nil || len(started) != 1 {
			t.Fatalf("subpath %q accepted: %v", bad, err)
		}
	}
	contained := params
	contained.Subpath = "packages/app"
	if _, err := broker.Run(t.Context(), d.ID, runEvent(t, contained)); err != nil || len(started) != 2 || started[1] != project+"|"+project+"/packages/app|conformance prompt|build|provider/model|read-only" {
		t.Fatalf("contained subpath denied: %v %v", err, started)
	}
	unknown := runEvent(t, params)
	unknown.Params = json.RawMessage(`{"prompt":"x","directory":"/etc"}`)
	if _, err := broker.Run(t.Context(), d.ID, unknown); err == nil || len(started) != 2 {
		t.Fatalf("unknown run parameter accepted: %v", err)
	}
	if err := broker.Completed(t.Context(), d.ID, "conformance-completed", completion); err != nil || len(calls) != 1 {
		t.Fatalf("granted completed failed: %v", err)
	}
	var sent plugins.SessionCompleted
	if json.Unmarshal(calls[0].Params, &sent) != nil || sent != completion {
		t.Fatalf("completed params %s", calls[0].Params)
	}
	// The stable operation id is the plugin's repeat-detector.
	if calls[0].OperationID != "conformance-completed" {
		t.Fatalf("completed lost its operation id: %q", calls[0].OperationID)
	}
	grants = nil
	if _, err := broker.Run(t.Context(), d.ID, runEvent(t, params)); err == nil || len(started) != 2 {
		t.Fatalf("revoked grant still started a session: %v", err)
	}
	c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
	c.exit()
}
