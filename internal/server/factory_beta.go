package server

import (
	"context"
	"maps"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/httpx"
)

const factoryBetaKey = "beta.factory"

type betaFeaturesView struct {
	Factory bool `json:"factory"`
}

func (s *Server) factoryBetaEnabled(ctx context.Context) bool {
	if s.stateDB == nil {
		return false
	}
	value, _, err := s.stateDB.GetSetting(ctx, factoryBetaKey)
	return err == nil && value == "true"
}

func (s *Server) betaFeaturesSetting() httpx.SettingRoute[betaFeaturesView] {
	return httpx.SettingRoute[betaFeaturesView]{
		Name: "beta features", Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			value, _, err := s.stateDB.GetSetting(ctx, factoryBetaKey)
			return betaFeaturesView{Factory: value == "true"}, err
		},
		Set: func(ctx context.Context, body betaFeaturesView) error {
			value := "false"
			if body.Factory {
				value = "true"
			}
			return s.stateDB.SetSetting(ctx, factoryBetaKey, value)
		},
	}
}

// Read at request time so the shared MCP handler follows saves without restart.
func (s *Server) filterBetaTools(ctx context.Context, tools []mcplib.Tool) []mcplib.Tool {
	if s.factoryBetaEnabled(ctx) {
		return tools
	}
	visible := make([]mcplib.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "factory" || tool.Name == "factory_control" {
			continue
		}
		if tool.Name == "inbox" {
			tool.InputSchema.Properties = maps.Clone(tool.InputSchema.Properties)
			tool.InputSchema.Properties["category"] = map[string]any{"type": "string", "enum": []string{"general", "routine"}}
		}
		visible = append(visible, tool)
	}
	return visible
}
