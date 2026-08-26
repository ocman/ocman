package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
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
		WorkflowService: workflows.NewService(workflows.Deps{Store: stateDB}),
	})
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestToolSetOmitsSessionManagement(t *testing.T) {
	retired := map[string]bool{
		"new_session": true, "await_session_result": true,
		"get_session_status": true, "get_current_session_id": true,
		"list_child_sessions": true, "cancel_session": true,
		"send_message_to_child": true, "send_message_to_parent": true,
	}
	want := map[string]bool{"embed_file": true, "validate_workflow": true}
	stateDB := openTestStateDB(t)
	for _, tool := range internalmcp.ServerTools(internalmcp.Deps{
		WorkflowService: workflows.NewService(workflows.Deps{Store: stateDB}),
	}) {
		if retired[tool.Tool.Name] {
			t.Errorf("retired session tool %q is still registered", tool.Tool.Name)
		}
		delete(want, tool.Tool.Name)
	}
	for name := range want {
		t.Errorf("retained MCP tool %q is not registered", name)
	}
}

func TestPublicHTTPToolRegistryIsExact(t *testing.T) {
	stateDB := openTestStateDB(t)
	httpServer := httptest.NewServer(internalmcp.New(internalmcp.Deps{
		WorkflowService: workflows.NewService(workflows.Deps{Store: stateDB}),
	}).Handler())
	defer httpServer.Close()

	client, err := mcpclient.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(ctx, mcplib.InitializeRequest{Params: mcplib.InitializeParams{
		ProtocolVersion: mcplib.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcplib.Implementation{Name: "ocman-test", Version: "1"},
	}}); err != nil {
		t.Fatal(err)
	}
	listed, err := client.ListTools(ctx, mcplib.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	want := []string{
		"approve_workflow_node", "cancel_workflow_run", "embed_file", "get_workflow_schema",
		"inspect_workflow_run", "list_workflow_runs", "list_workflows", "pause_workflow_run",
		"publish_workflow", "resolve_unknown_attempt", "resume_workflow_run", "retry_workflow_from_node",
		"start_workflow", "validate_workflow",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
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

func TestWorkflowToolsRejectMissingArguments(t *testing.T) {
	srv := buildWorkflowMCPServer(t, openTestStateDB(t))
	tests := []struct {
		name string
		args map[string]interface{}
	}{
		{"validate_workflow", nil},
		{"publish_workflow", nil},
		{"start_workflow", nil},
		{"inspect_workflow_run", nil},
		{"pause_workflow_run", nil},
		{"resume_workflow_run", nil},
		{"cancel_workflow_run", nil},
		{"approve_workflow_node", map[string]interface{}{"run_id": "run"}},
		{"resolve_unknown_attempt", map[string]interface{}{"run_id": "run", "attempt_id": 0, "resolution": "successful"}},
		{"resolve_unknown_attempt", map[string]interface{}{"run_id": "run", "attempt_id": 1}},
		{"retry_workflow_from_node", map[string]interface{}{"run_id": "run"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if result := callTool(t, srv, tt.name, tt.args); !result.IsError {
				t.Fatalf("result = %+v, want tool error", result)
			}
		})
	}
}

type failingWorkflowService struct{ err error }

func (f failingWorkflowService) ValidateJSON(context.Context, []byte) (workflows.Definition, error) {
	return workflows.Definition{}, f.err
}
func (f failingWorkflowService) PublishJSON(context.Context, []byte) (workflows.Version, error) {
	return workflows.Version{}, f.err
}
func (f failingWorkflowService) ListVersions(context.Context) ([]workflows.Version, error) {
	return nil, f.err
}
func (f failingWorkflowService) Start(context.Context, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) ListRuns(context.Context) ([]workflows.Run, error) {
	return nil, f.err
}
func (f failingWorkflowService) GetRun(context.Context, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) Pause(context.Context, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) Resume(context.Context, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) Cancel(context.Context, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) Approve(context.Context, string, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) ResolveUnknown(context.Context, string, int64, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}
func (f failingWorkflowService) RetryFrom(context.Context, string, string, string) (workflows.RunDetail, error) {
	return workflows.RunDetail{}, f.err
}

func TestWorkflowToolsSurfaceServiceErrors(t *testing.T) {
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{
		WorkflowService: failingWorkflowService{err: errors.New("store unavailable")},
	})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	tests := []struct {
		name string
		args map[string]interface{}
	}{
		{"validate_workflow", map[string]interface{}{"definition": `{}`}},
		{"publish_workflow", map[string]interface{}{"definition": `{}`}},
		{"list_workflows", nil},
		{"start_workflow", map[string]interface{}{"version_id": "v1"}},
		{"list_workflow_runs", nil},
		{"inspect_workflow_run", map[string]interface{}{"run_id": "run"}},
		{"pause_workflow_run", map[string]interface{}{"run_id": "run"}},
		{"resume_workflow_run", map[string]interface{}{"run_id": "run"}},
		{"cancel_workflow_run", map[string]interface{}{"run_id": "run"}},
		{"approve_workflow_node", map[string]interface{}{"run_id": "run", "node_id": "node"}},
		{"resolve_unknown_attempt", map[string]interface{}{"run_id": "run", "attempt_id": 1, "resolution": "successful"}},
		{"retry_workflow_from_node", map[string]interface{}{"run_id": "run", "node_id": "node"}},
	}
	for _, tt := range tests {
		if result := callTool(t, srv, tt.name, tt.args); !result.IsError || !strings.Contains(resultText(result), "store unavailable") {
			t.Fatalf("%s result = %+v", tt.name, result)
		}
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
		"retry_workflow_from_node": true,
	}
	for _, tool := range internalmcp.ServerTools(internalmcp.Deps{WorkflowService: workflows.NewService(workflows.Deps{Store: openTestStateDB(t)})}) {
		delete(want, tool.Tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing workflow tools: %#v", want)
	}
}

func TestRetryWorkflowFromNode(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := buildWorkflowMCPServer(t, stateDB)
	published := resultObject(t, srv, "publish_workflow", map[string]interface{}{"definition": workflowDefinition})
	started := resultObject(t, srv, "start_workflow", map[string]interface{}{"version_id": published["version_id"]})
	runID := started["run_id"].(string)
	resultObject(t, srv, "approve_workflow_node", map[string]interface{}{"run_id": runID, "node_id": "review"})
	resultObject(t, srv, "approve_workflow_node", map[string]interface{}{"run_id": runID, "node_id": "ship"})
	adjusted := strings.Replace(workflowDefinition, `"name":"Ship"`, `"name":"Ship adjusted"`, 1)
	v2 := resultObject(t, srv, "publish_workflow", map[string]interface{}{"definition": adjusted})
	retried := resultObject(t, srv, "retry_workflow_from_node", map[string]interface{}{"run_id": runID, "node_id": "ship", "version_id": v2["version_id"]})
	if retried["retry_of_run_id"] != runID || retried["retry_from_node_id"] != "ship" || retried["version_id"] != v2["version_id"] {
		t.Fatalf("retry result = %#v", retried)
	}
	inspected := resultObject(t, srv, "inspect_workflow_run", map[string]interface{}{"run_id": retried["run_id"]})
	if !strings.Contains(fmt.Sprint(inspected), "reused_attempt_id") {
		t.Fatalf("retry provenance missing from inspection: %#v", inspected)
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
