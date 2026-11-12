package mcp

import (
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/mcpauth"
)

// SessionCaller is transport correlation, not authentication or execution
// authority. The service must prove its owner/source in the session catalogue.
type SessionCaller struct {
	Kind      string
	SessionID string
}

func sessionCaller(req mcplib.CallToolRequest) (*SessionCaller, error) {
	kind, id := req.Header.Get(mcpauth.CallerPlatformHeader), req.Header.Get(mcpauth.CallerSessionHeader)
	var caller *SessionCaller
	if kind != "" || id != "" {
		if strings.TrimSpace(kind) != kind || strings.TrimSpace(id) != id || kind == "" || id == "" {
			return nil, fmt.Errorf("invalid MCP caller headers")
		}
		caller = &SessionCaller{Kind: kind, SessionID: id}
	}
	if req.Params.Meta != nil {
		if value, present := req.Params.Meta.AdditionalFields["ai.opencode/sessionID"]; present {
			id, ok := value.(string)
			if !ok || id == "" || strings.TrimSpace(id) != id {
				return nil, fmt.Errorf("invalid OpenCode caller metadata")
			}
			native := SessionCaller{Kind: "opencode", SessionID: id}
			if caller != nil && *caller != native {
				return nil, fmt.Errorf("conflicting MCP caller context")
			}
			caller = &native
		}
	}
	return caller, nil
}
