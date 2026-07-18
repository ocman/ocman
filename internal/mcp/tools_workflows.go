package mcp

import (
	"context"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

type workflowService interface {
	ValidateJSON(context.Context, []byte) (workflows.Definition, error)
	PublishJSON(context.Context, []byte) (workflows.Version, error)
	ListVersions(context.Context) ([]workflows.Version, error)
	Start(context.Context, string) (workflows.RunDetail, error)
	ListRuns(context.Context) ([]workflows.Run, error)
	GetRun(context.Context, string) (workflows.RunDetail, error)
	Pause(context.Context, string) (workflows.RunDetail, error)
	Resume(context.Context, string) (workflows.RunDetail, error)
	Cancel(context.Context, string) (workflows.RunDetail, error)
	Approve(context.Context, string, string) (workflows.RunDetail, error)
	ResolveUnknown(context.Context, string, int64, string) (workflows.RunDetail, error)
}

type workflowTools struct{ svc workflowService }

const workflowDefinitionSchema = `Workflow definition JSON. Required top-level fields: id, name, version, concurrency (>0), triggers (at least one), nodes (at least one), dependencies. Start with a manual trigger: {"id":"manual","type":"manual"}. Each node needs id, name, and type.

Node types and configuration:
- approval: no additional configuration.
- command: command is a string array; the workflow directory must be an existing absolute path. Optional permission entries are {"permission":"bash","pattern":"...","action":"allow"|"deny"|"ask"}. Successful commands must write exactly one JSON value to stdout.
- agent: agent requires {"directory":"...","prompt":"..."}; optional platform, model, agent, reasoning, sessionAffinity, sessionId, and outputSchema. Without outputSchema, any completed response succeeds. With outputSchema, it must be a valid JSON Schema; the runtime includes it in the prompt, strips a surrounding Markdown code fence, validates the response against it, and gives one corrective retry.
- subworkflow: subworkflow requires {"workflowId":"published-workflow-id"}.
- map: map requires {"source":"${nodes.upstream.output}","key":"stable-item-field","subworkflow":{"workflowId":"..."},"join":"join-node-id"}; source must be an upstream node whose output is an array.
- join: join requires {"policy":"all-success"|"always"|"minimum-success"}; minimum-success also requires minSuccess > 0.

Dependencies are {"from":"upstream-node-id","to":"downstream-node-id"} and must form a DAG. Command arguments, environment values, and agent prompts can interpolate transitive dependencies with ${nodes.<id>.output...}; inserted values remain JSON. Optional top-level fields include directory, pools, workspace, limits, retentionDays, and failFast. Validate before publishing.

Minimal valid definition: {"id":"example","name":"Example","version":"1","concurrency":1,"triggers":[{"id":"manual","type":"manual"}],"nodes":[{"id":"approve","name":"Approve","type":"approval"}],"dependencies":[]}.`

func addWorkflowTools(s *server.MCPServer, t *workflowTools) {
	for _, tool := range workflowServerTools(t) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func workflowServerTools(t *workflowTools) []server.ServerTool {
	if t == nil || t.svc == nil {
		return nil
	}
	return []server.ServerTool{
		{Tool: mcplib.NewTool("get_workflow_schema", mcplib.WithDescription("Get the workflow definition schema and minimal valid JSON example before creating or validating a workflow.")), Handler: t.handleSchema},
		{Tool: workflowDefinitionTool("validate_workflow", "Validate a workflow JSON definition without publishing it."), Handler: t.handleValidate},
		{Tool: workflowDefinitionTool("publish_workflow", "Validate and publish an immutable workflow version."), Handler: t.handlePublish},
		{Tool: mcplib.NewTool("list_workflows", mcplib.WithDescription("List published workflow versions.")), Handler: t.handleListWorkflows},
		{Tool: mcplib.NewTool("start_workflow", mcplib.WithDescription("Start a pinned version or a workflow's active version."), mcplib.WithString("version_id", mcplib.Description("Immutable workflow version ID.")), mcplib.WithString("workflow_id", mcplib.Description("Workflow ID; starts its active version."))), Handler: t.handleStart},
		{Tool: mcplib.NewTool("list_workflow_runs", mcplib.WithDescription("List workflow runs."), mcplib.WithString("workflow_id", mcplib.Description("Optional workflow ID filter."))), Handler: t.handleListRuns},
		{Tool: workflowRunTool("inspect_workflow_run", "Inspect a workflow run, nodes, attempts, and artifact metadata."), Handler: t.handleInspect},
		{Tool: workflowRunTool("pause_workflow_run", "Pause new scheduling for a workflow run."), Handler: t.control("pause")},
		{Tool: workflowRunTool("resume_workflow_run", "Resume a paused workflow run."), Handler: t.control("resume")},
		{Tool: workflowRunTool("cancel_workflow_run", "Cancel a workflow run."), Handler: t.control("cancel")},
		{Tool: mcplib.NewTool("approve_workflow_node", mcplib.WithDescription("Approve a waiting workflow node."), mcplib.WithString("run_id", mcplib.Required()), mcplib.WithString("node_id", mcplib.Required())), Handler: t.handleApprove},
		{Tool: mcplib.NewTool("resolve_unknown_attempt", mcplib.WithDescription("Resolve an unknown workflow attempt after inspection."), mcplib.WithString("run_id", mcplib.Required()), mcplib.WithNumber("attempt_id", mcplib.Required()), mcplib.WithString("resolution", mcplib.Required(), mcplib.Enum(workflows.AttemptSuccessful, workflows.AttemptFailed))), Handler: t.handleResolveUnknown},
	}
}

func workflowDefinitionTool(name, description string) mcplib.Tool {
	return mcplib.NewTool(name, mcplib.WithDescription(description), mcplib.WithString("definition", mcplib.Required(), mcplib.Description(workflowDefinitionSchema)))
}

func workflowRunTool(name, description string) mcplib.Tool {
	return mcplib.NewTool(name, mcplib.WithDescription(description), mcplib.WithString("run_id", mcplib.Required(), mcplib.Description("Workflow run ID.")))
}

func (t *workflowTools) handleSchema(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	return mcplib.NewToolResultText(workflowDefinitionSchema), nil
}

func (t *workflowTools) handleValidate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	source, err := req.RequireString("definition")
	if err != nil {
		return mcplib.NewToolResultError("definition is required"), nil
	}
	definition, err := t.svc.ValidateJSON(ctx, []byte(source))
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(map[string]interface{}{"valid": true, "workflow_id": definition.ID, "name": definition.Name, "version": definition.Version, "node_count": len(definition.Nodes)}), nil
}

func (t *workflowTools) handlePublish(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	source, err := req.RequireString("definition")
	if err != nil {
		return mcplib.NewToolResultError("definition is required"), nil
	}
	version, err := t.svc.PublishJSON(ctx, []byte(source))
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(workflowVersionSummary(version)), nil
}

func (t *workflowTools) handleListWorkflows(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	versions, err := t.svc.ListVersions(ctx)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	out := make([]map[string]interface{}, 0, len(versions))
	for _, version := range versions {
		out = append(out, workflowVersionSummary(version))
	}
	return toolResultJSON(out), nil
}

func (t *workflowTools) handleStart(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id := req.GetString("version_id", "")
	if id == "" {
		id = req.GetString("workflow_id", "")
	}
	if id == "" {
		return mcplib.NewToolResultError("workflow_id or version_id is required"), nil
	}
	run, err := t.svc.Start(ctx, id)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(workflowRunSummary(run.Run)), nil
}

func (t *workflowTools) handleListRuns(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	runs, err := t.svc.ListRuns(ctx)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	workflowID := req.GetString("workflow_id", "")
	out := make([]map[string]interface{}, 0, len(runs))
	for _, run := range runs {
		if workflowID == "" || run.WorkflowID == workflowID {
			out = append(out, workflowRunSummary(run))
		}
	}
	return toolResultJSON(out), nil
}

func (t *workflowTools) handleInspect(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	runID, err := req.RequireString("run_id")
	if err != nil {
		return mcplib.NewToolResultError("run_id is required"), nil
	}
	run, err := t.svc.GetRun(ctx, runID)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(workflowRunDetail(run)), nil
}

func (t *workflowTools) control(action string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		runID, err := req.RequireString("run_id")
		if err != nil {
			return mcplib.NewToolResultError("run_id is required"), nil
		}
		var run workflows.RunDetail
		switch action {
		case "pause":
			run, err = t.svc.Pause(ctx, runID)
		case "resume":
			run, err = t.svc.Resume(ctx, runID)
		case "cancel":
			run, err = t.svc.Cancel(ctx, runID)
		}
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		return toolResultJSON(workflowRunSummary(run.Run)), nil
	}
}

func (t *workflowTools) handleApprove(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	runID, err := req.RequireString("run_id")
	if err != nil {
		return mcplib.NewToolResultError("run_id is required"), nil
	}
	nodeID, err := req.RequireString("node_id")
	if err != nil {
		return mcplib.NewToolResultError("node_id is required"), nil
	}
	run, err := t.svc.Approve(ctx, runID, nodeID)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(workflowRunSummary(run.Run)), nil
}

func (t *workflowTools) handleResolveUnknown(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	runID, err := req.RequireString("run_id")
	if err != nil {
		return mcplib.NewToolResultError("run_id is required"), nil
	}
	attemptID, err := req.RequireInt("attempt_id")
	if err != nil || attemptID <= 0 {
		return mcplib.NewToolResultError("attempt_id must be a positive integer"), nil
	}
	resolution, err := req.RequireString("resolution")
	if err != nil {
		return mcplib.NewToolResultError("resolution is required"), nil
	}
	run, err := t.svc.ResolveUnknown(ctx, runID, int64(attemptID), resolution)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(workflowRunSummary(run.Run)), nil
}

func workflowVersionSummary(version workflows.Version) map[string]interface{} {
	return map[string]interface{}{"workflow_id": version.WorkflowID, "version_id": version.ID, "name": version.Name, "revision": version.Revision, "version": version.Definition.Version, "created_at": version.CreatedAt}
}

func workflowRunSummary(run workflows.Run) map[string]interface{} {
	return map[string]interface{}{"run_id": run.ID, "workflow_id": run.WorkflowID, "version_id": run.VersionID, "state": run.State, "created_at": run.CreatedAt, "updated_at": run.UpdatedAt}
}

func workflowRunDetail(run workflows.RunDetail) map[string]interface{} {
	out := workflowRunSummary(run.Run)
	nodes := make([]map[string]interface{}, 0, len(run.Nodes))
	for _, node := range run.Nodes {
		attempts := make([]map[string]interface{}, 0, len(node.Attempts))
		for _, attempt := range node.Attempts {
			attempts = append(attempts, map[string]interface{}{"attempt_id": attempt.ID, "sequence": attempt.Seq, "state": attempt.State, "started_at": attempt.StartedAt, "completed_at": attempt.CompletedAt})
		}
		nodes = append(nodes, map[string]interface{}{"node_id": node.NodeID, "name": node.Name, "type": node.Type, "state": node.State, "attempts": attempts})
	}
	out["nodes"] = nodes
	// Artifact persistence arrives in a later workflow-engine slice. Keep the
	// contract metadata-only so future storage can be added without exposing values.
	out["artifacts"] = []interface{}{}
	return out
}
