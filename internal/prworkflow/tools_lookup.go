package prworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func sessionPRAction(action string) bool {
	switch action {
	case "linked", "link", "unlink", "watch", "unwatch", "feedback", "checks_prompt", "conflicts_prompt":
		return true
	}
	return false
}

func (m *Module) lookupToolSession(ctx context.Context, req mcplib.CallToolRequest) (mcplib.CallToolRequest, error) {
	if !sessionPRAction(req.GetString("action", "")) {
		return req, nil
	}
	id, platform := req.GetString("session_id", ""), req.GetString("platform", "")
	if strings.TrimSpace(id) != "" && strings.TrimSpace(platform) != "" {
		return req, nil
	}
	// Leave missing directory and malformed identity arguments to normal validation.
	directory := req.GetString("directory", "")
	if directory == "" {
		return req, nil
	}
	for _, key := range []string{"session_id", "platform", "remote_id"} {
		if value, exists := req.GetArguments()[key]; exists {
			if _, ok := value.(string); !ok {
				return req, fmt.Errorf("%s must be a string", key)
			}
		}
	}
	if !filepath.IsAbs(directory) {
		return req, fmt.Errorf("directory must be absolute for session lookup")
	}
	if m.d.FindSessions == nil {
		return req, fmt.Errorf("session lookup unavailable; supply session_id and platform")
	}
	owner := req.GetString("remote_id", "")
	candidates, err := m.d.FindSessions(ctx, directory, platform, owner)
	if err != nil {
		return req, fmt.Errorf("session lookup failed; no PR action was performed")
	}
	var matches []Target
	for _, candidate := range candidates {
		if candidate.SessionID != "" && candidate.Platform != "" && candidate.Directory == directory &&
			(id == "" || candidate.SessionID == id) && (platform == "" || candidate.Platform == platform) &&
			(owner == "" || candidate.RemoteID == owner) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return req, fmt.Errorf("no session matches the exact directory and identity scope; supply session_id and platform")
	}
	if len(matches) > 1 {
		// Return candidates in the same response, without picking by recency or status.
		choices := make([]map[string]string, 0, min(len(matches), 20))
		for _, match := range matches[:min(len(matches), 20)] {
			choices = append(choices, map[string]string{"session_id": match.SessionID, "platform": match.Platform})
		}
		encoded, _ := json.Marshal(choices)
		return req, fmt.Errorf("session lookup is ambiguous (%d matches); supply session_id and platform. Candidates (up to 20): %s", len(matches), encoded)
	}
	args := maps.Clone(req.GetArguments())
	args["session_id"], args["platform"] = matches[0].SessionID, matches[0].Platform
	req.Params.Arguments = args
	return req, nil
}
