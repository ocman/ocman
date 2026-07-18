package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

const workflowDefinition = `{
	"id":"release","name":"Release","version":"1","concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[{"id":"review","name":"Review","type":"approval"},{"id":"ship","name":"Ship","type":"approval"}],
	"dependencies":[{"from":"review","to":"ship"}]
}`

func buildWorkflowMCPServer(t *testing.T, stateDB *state.DB) *mcptest.Server {
	t.Helper()
	tools := internalmcp.ServerTools(internalmcp.Deps{
		StateDB:         stateDB,
		PlatformID:      "opencode",
		WorkflowService: workflows.NewService(workflows.Deps{Store: stateDB}),
	})
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func resultObject(t *testing.T, srv *mcptest.Server, name string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	result := callTool(t, srv, name, args)
	if result.IsError {
		t.Fatalf("%s: %s", name, resultText(result))
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(result)), &out); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return out
}

func TestWorkflowToolsLifecycle(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := buildWorkflowMCPServer(t, stateDB)

	validated := resultObject(t, srv, "validate_workflow", map[string]interface{}{"definition": workflowDefinition})
	if validated["valid"] != true || validated["workflow_id"] != "release" || validated["node_count"] != float64(2) {
		t.Fatalf("unexpected validation result: %#v", validated)
	}

	published := resultObject(t, srv, "publish_workflow", map[string]interface{}{"definition": workflowDefinition})
	versionID, _ := published["version_id"].(string)
	if versionID == "" || published["workflow_id"] != "release" || published["revision"] != float64(1) {
		t.Fatalf("unexpected publish result: %#v", published)
	}

	listed := callTool(t, srv, "list_workflows", map[string]interface{}{})
	if listed.IsError || !strings.Contains(resultText(listed), versionID) {
		t.Fatalf("list_workflows: %s", resultText(listed))
	}

	// A workflow ID selects its current active version; version_id pins one.
	started := resultObject(t, srv, "start_workflow", map[string]interface{}{"workflow_id": "release"})
	runID, _ := started["run_id"].(string)
	if runID == "" || started["version_id"] != versionID || started["state"] != workflows.StateActive {
		t.Fatalf("unexpected start result: %#v", started)
	}

	runs := callTool(t, srv, "list_workflow_runs", map[string]interface{}{"workflow_id": "release"})
	if runs.IsError || !strings.Contains(resultText(runs), runID) {
		t.Fatalf("list_workflow_runs: %s", resultText(runs))
	}

	inspected := resultObject(t, srv, "inspect_workflow_run", map[string]interface{}{"run_id": runID})
	if _, ok := inspected["artifacts"].([]interface{}); !ok {
		t.Fatalf("inspection must contain metadata-only artifacts list: %#v", inspected)
	}
	nodes, ok := inspected["nodes"].([]interface{})
	if !ok || len(nodes) != 2 || !strings.Contains(resultText(callTool(t, srv, "inspect_workflow_run", map[string]interface{}{"run_id": runID})), "attempt_id") {
		t.Fatalf("inspection omitted nodes/attempt IDs: %#v", inspected)
	}

	paused := resultObject(t, srv, "pause_workflow_run", map[string]interface{}{"run_id": runID})
	if paused["state"] != workflows.StatePaused {
		t.Fatalf("pause state: %#v", paused)
	}
	resumed := resultObject(t, srv, "resume_workflow_run", map[string]interface{}{"run_id": runID})
	if resumed["state"] != workflows.StateActive {
		t.Fatalf("resume state: %#v", resumed)
	}
	approved := resultObject(t, srv, "approve_workflow_node", map[string]interface{}{"run_id": runID, "node_id": "review"})
	if approved["state"] != workflows.StateActive {
		t.Fatalf("approve state: %#v", approved)
	}
	canceled := resultObject(t, srv, "cancel_workflow_run", map[string]interface{}{"run_id": runID})
	if canceled["state"] != workflows.StateCanceled {
		t.Fatalf("cancel state: %#v", canceled)
	}
}

func TestWorkflowDefinitionToolsDescribeSchema(t *testing.T) {
	tools := internalmcp.ServerTools(internalmcp.Deps{WorkflowService: workflows.NewService(workflows.Deps{Store: openTestStateDB(t)})})
	found := 0
	for _, tool := range tools {
		if tool.Tool.Name != "validate_workflow" && tool.Tool.Name != "publish_workflow" {
			continue
		}
		found++
		schema, err := json.Marshal(tool.Tool.InputSchema.Properties["definition"])
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"Node types and configuration", "subworkflow", "minimum-success", "Minimal valid definition"} {
			if !strings.Contains(string(schema), expected) {
				t.Fatalf("%s definition schema missing %q: %s", tool.Tool.Name, expected, schema)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d workflow definition tools, want 2", found)
	}
}

func TestWorkflowToolRegistrationIsComplete(t *testing.T) {
	want := map[string]bool{
		"get_workflow_schema": true, "validate_workflow": true, "publish_workflow": true,
		"list_workflows": true, "start_workflow": true, "list_workflow_runs": true,
		"inspect_workflow_run": true, "pause_workflow_run": true, "resume_workflow_run": true,
		"cancel_workflow_run": true, "approve_workflow_node": true, "resolve_unknown_attempt": true,
	}
	for _, tool := range internalmcp.ServerTools(internalmcp.Deps{WorkflowService: workflows.NewService(workflows.Deps{Store: openTestStateDB(t)})}) {
		delete(want, tool.Tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing workflow tools: %#v", want)
	}
}

func TestGetWorkflowSchema(t *testing.T) {
	srv := buildWorkflowMCPServer(t, openTestStateDB(t))
	result := callTool(t, srv, "get_workflow_schema", map[string]interface{}{})
	if result.IsError || !strings.Contains(resultText(result), "Minimal valid definition") || !strings.Contains(resultText(result), "outputSchema") {
		t.Fatalf("get_workflow_schema: %s", resultText(result))
	}
}

func TestWorkflowToolsReturnActionableErrors(t *testing.T) {
	srv := buildWorkflowMCPServer(t, openTestStateDB(t))

	invalid := callTool(t, srv, "validate_workflow", map[string]interface{}{"definition": `{"id":"broken"}`})
	if !invalid.IsError || !strings.Contains(resultText(invalid), "name, and version are required") {
		t.Fatalf("unexpected validation error: %q", resultText(invalid))
	}

	missing := callTool(t, srv, "start_workflow", map[string]interface{}{})
	if !missing.IsError || !strings.Contains(resultText(missing), "workflow_id or version_id is required") {
		t.Fatalf("unexpected start error: %q", resultText(missing))
	}

	unknown := callTool(t, srv, "resolve_unknown_attempt", map[string]interface{}{
		"run_id": "missing", "attempt_id": 42, "resolution": "successful",
	})
	if !unknown.IsError || !strings.Contains(resultText(unknown), "unknown attempt") {
		t.Fatalf("unexpected resolve error: %q", resultText(unknown))
	}
}
