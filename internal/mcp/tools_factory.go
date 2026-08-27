package mcp

import (
	"context"
	"errors"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/factory"
)

type factoryService interface {
	PrepareWork(context.Context, factory.PrepareWorkRequest) (factory.PreparedWork, error)
	AcknowledgeLocalExecution(context.Context, string) error
	CreatePreparedWorkEpic(context.Context, factory.PreparedWork) (factory.WorkEpic, error)
}

type factoryTools struct{ svc factoryService }

func addFactoryTools(s *server.MCPServer, tools *factoryTools) {
	for _, tool := range factoryServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func factoryServerTools(tools *factoryTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{
		{
			Tool: mcplib.NewTool("prepare_factory_work",
				mcplib.WithDescription("Prepare an explicit conversation handoff to Factory without creating work. Present the returned proposal to the user for confirmation."),
				mcplib.WithString("goal", mcplib.Required(), mcplib.Description("Short delivery goal.")),
				mcplib.WithString("brief", mcplib.Required(), mcplib.Description("Confirmed Markdown summary of objectives, constraints, and decisions; never include a transcript.")),
				mcplib.WithString("project_path", mcplib.Required(), mcplib.Description("Absolute path within the target local Git repository."))),
			Handler: tools.handlePrepare,
		},
		{
			Tool: mcplib.NewTool("acknowledge_factory_execution",
				mcplib.WithDescription("Record the user's explicit acknowledgement that Factory commands for this project run locally without process isolation. Call only after showing this warning."),
				mcplib.WithString("project_path", mcplib.Required(), mcplib.Description("Canonical project path returned by prepare_factory_work."))),
			Handler: tools.handleAcknowledge,
		},
		{
			Tool: mcplib.NewTool("create_factory_work_epic",
				mcplib.WithDescription("Create the exact confirmed Factory Work Epic. Reuse the same preparation key for retries, then stop the originating conversation after success."),
				mcplib.WithString("preparation_key", mcplib.Required()),
				mcplib.WithString("goal", mcplib.Required()),
				mcplib.WithString("brief", mcplib.Required()),
				mcplib.WithString("project_path", mcplib.Required())),
			Handler: tools.handleCreate,
		},
	}
}

func (t *factoryTools) handlePrepare(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	goal, brief, project, result := factoryInputs(req)
	if result != nil {
		return result, nil
	}
	prepared, err := t.svc.PrepareWork(ctx, factory.PrepareWorkRequest{Goal: goal, Brief: brief, ProjectPath: project})
	if err != nil {
		return factoryToolError(err), nil
	}
	return toolResultJSON(map[string]interface{}{
		"preparation_key":          prepared.PreparationKey,
		"goal":                     prepared.Goal,
		"brief":                    prepared.Brief,
		"project_path":             prepared.ProjectPath,
		"formula":                  map[string]interface{}{"id": prepared.Formula.ID, "version": prepared.Formula.Version},
		"acknowledgement_required": prepared.AcknowledgementRequired,
	}), nil
}

func (t *factoryTools) handleAcknowledge(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	project, err := req.RequireString("project_path")
	if err != nil || project == "" {
		return mcplib.NewToolResultError("project_path is required"), nil
	}
	if err := t.svc.AcknowledgeLocalExecution(ctx, project); err != nil {
		return factoryToolError(err), nil
	}
	return toolResultJSON(map[string]interface{}{"acknowledged": true, "project_path": project}), nil
}

func (t *factoryTools) handleCreate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	goal, brief, project, result := factoryInputs(req)
	if result != nil {
		return result, nil
	}
	key, err := req.RequireString("preparation_key")
	if err != nil || key == "" {
		return mcplib.NewToolResultError("preparation_key is required"), nil
	}
	epic, err := t.svc.CreatePreparedWorkEpic(ctx, factory.PreparedWork{
		PreparationKey: key,
		Goal:           goal,
		Brief:          brief,
		ProjectPath:    project,
		Formula:        factory.DefaultFormula(),
	})
	if err != nil {
		return factoryToolError(err), nil
	}
	return toolResultJSON(map[string]interface{}{
		"work_epic_id": epic.ID,
		"status":       epic.Status,
		"planning": map[string]interface{}{
			"work_id":          epic.Planning.WorkID,
			"work_status":      epic.Planning.WorkStatus,
			"approval_gate_id": epic.Planning.ApprovalGateID,
			"approval_status":  epic.Planning.ApprovalStatus,
		},
		"mission_control_path": "/factory",
		"handoff_complete":     true,
	}), nil
}

func factoryInputs(req mcplib.CallToolRequest) (string, string, string, *mcplib.CallToolResult) {
	goal, err := req.RequireString("goal")
	if err != nil || goal == "" {
		return "", "", "", mcplib.NewToolResultError("goal is required")
	}
	brief, err := req.RequireString("brief")
	if err != nil || brief == "" {
		return "", "", "", mcplib.NewToolResultError("brief is required")
	}
	project, err := req.RequireString("project_path")
	if err != nil || project == "" {
		return "", "", "", mcplib.NewToolResultError("project_path is required")
	}
	return goal, brief, project, nil
}

func factoryToolError(err error) *mcplib.CallToolResult {
	message := "factory_unavailable: Open Mission Control for diagnostics."
	switch {
	case errors.Is(err, factory.ErrProjectNotLocalGit):
		message = "project_not_local_git: The project must be an existing local Git repository."
	case errors.Is(err, factory.ErrPreparationStale):
		message = "factory_preparation_stale: Prepare and confirm the handoff again."
	case errors.Is(err, factory.ErrAcknowledgementRequired):
		message = "factory_acknowledgement_required: Show the local execution warning and obtain explicit acknowledgement."
	case errors.Is(err, factory.ErrInstantiationConflict):
		message = "factory_intake_conflict: This preparation belongs to different confirmed inputs."
	default:
		log.WithError(err).Warn("mcp: Factory tool failed")
	}
	return mcplib.NewToolResultError(message)
}
