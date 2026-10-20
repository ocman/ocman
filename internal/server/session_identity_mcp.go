package server

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/NoUseFreak/ocman/internal/sessionview"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func toolSessionShape(name string) *sessionview.Shape {
	switch name {
	case "routines":
		return routineSessionShape
	case "artifacts":
		return artifactSessionShape
	case "inbox":
		return inboxResponseShape
	case "factory", "factory_control":
		return factorySessionShape
	case "webhooks":
		return webhookSessionShape
	default:
		return nil
	}
}

// Feature tools keep native service/storage keys internally, just like REST.
// Only registered normalized arguments and JSON results cross this boundary;
// prompts, free-form text, file content and opaque metadata are never rewritten.
func (s *Server) sessionIdentityToolMiddleware(next mcpserver.ToolHandlerFunc) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		shape := toolSessionShape(req.Params.Name)
		if s.sessionCatalogue == nil || shape == nil || req.GetString("action", "") == "help" {
			return next(ctx, req)
		}
		args := maps.Clone(req.GetArguments())
		platform, _ := args["platform"].(string)
		remote, _ := args["remote_id"].(string)
		scope := newSessionReferenceScope(platform, remote)
		for _, field := range []string{"session_id", "parent_session_id", "owner_session_ids"} {
			resolve := func(id string) (string, error) {
				if !strings.HasPrefix(id, "ocm_") {
					return id, nil
				}
				nativeID, err := scope.resolve(ctx, s, id, false)
				if err != nil {
					return "", err
				}
				args["platform"] = scope.platform
				return nativeID, nil
			}
			switch value := args[field].(type) {
			case string:
				id, err := resolve(value)
				if err != nil {
					return mcplib.NewToolResultError("resolving session identity: " + err.Error()), nil
				}
				args[field] = id
			case []any:
				ids := slices.Clone(value)
				for i, value := range ids {
					id, ok := value.(string)
					if !ok {
						continue
					}
					resolved, err := resolve(id)
					if err != nil {
						return mcplib.NewToolResultError("resolving session identity: " + err.Error()), nil
					}
					ids[i] = resolved
				}
				args[field] = ids
			}
		}
		req.Params.Arguments = args
		result, err := next(ctx, req)
		if err != nil || result == nil || result.IsError {
			return result, err
		}
		copy := *result
		copy.Content = slices.Clone(result.Content)
		p := s.publicSessionProjector(scope.platform, shape, false)
		for i, content := range copy.Content {
			if text, ok := content.(mcplib.TextContent); ok && json.Valid([]byte(text.Text)) {
				raw, err := p.JSON(ctx, []byte(text.Text))
				if err != nil {
					return mcplib.NewToolResultError("projecting session identity: " + err.Error()), nil
				}
				text.Text = string(raw)
				copy.Content[i] = text
			}
		}
		if copy.StructuredContent != nil {
			raw, err := json.Marshal(copy.StructuredContent)
			if err != nil {
				return nil, err
			}
			raw, err = p.JSON(ctx, raw)
			if err != nil {
				return mcplib.NewToolResultError("projecting session identity: " + err.Error()), nil
			}
			copy.StructuredContent = json.RawMessage(raw)
		}
		return &copy, nil
	}
}
