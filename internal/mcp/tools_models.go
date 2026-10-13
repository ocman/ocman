package mcp

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func modelServerTools(deps Deps) []server.ServerTool {
	if deps.ModelCatalog == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("models",
		mcplib.WithDescription("List models and their availability for a project directory without starting an agent. When hasProviders is true, use entries with isAvailable true; combine provider and model as provider/model for sessions create. When hasProviders is false, availability is unconfirmed and the catalog falls back to historical models and favorites. Pass platform when multiple machines are connected."),
		mcplib.WithReadOnlyHintAnnotation(true),
		mcplib.WithString("directory", mcplib.Required(), mcplib.Description("Absolute project directory on the target machine.")),
		mcplib.WithString("platform", mcplib.Description("Target platform ID, including the compound ID for a remote machine. Omit only when one platform is available."))),
		Handler: func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			directory, err := req.RequireString("directory")
			if err != nil || strings.TrimSpace(directory) == "" {
				return mcplib.NewToolResultError("directory is required"), nil
			}
			directory = strings.TrimSpace(directory)
			if !filepath.IsAbs(directory) {
				return mcplib.NewToolResultError("directory must be absolute"), nil
			}
			platform, err := req.RequireString("platform")
			if _, present := req.GetArguments()["platform"]; present && err != nil {
				return mcplib.NewToolResultError("platform must be a string"), nil
			}
			catalog, platform, err := deps.ModelCatalog(ctx, strings.TrimSpace(platform), platforms.DirectoryCatalogRequest{Directory: directory})
			if err != nil {
				return mcplib.NewToolResultError("model catalog request failed; check directory and platform"), nil
			}
			if catalog == nil {
				catalog = &platforms.DirectoryCatalog{}
			}
			models := platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
			if catalog.Models != nil {
				models = *catalog.Models
				if models.Models == nil {
					models.Models = []platforms.SessionModel{}
				}
			}
			return toolResultJSON(struct {
				platforms.SessionModelsResponse
				Platform       string `json:"platform"`
				DefaultModel   string `json:"defaultModel,omitempty"`
				LiveConnection bool   `json:"liveConnection"`
			}{models, platform, catalog.DefaultModel, catalog.LiveConnection}), nil
		}}}
}
