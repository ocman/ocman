package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- Session-scoped read endpoints ---

// listViaAdapter collapses the shared shape of the session-scoped list
// endpoints (agents, commands, questions): resolve the adapter, call
// one list method, nil-guard to an empty JSON array, write the result.
// A free function because Go methods can't have type parameters.
func listViaAdapter[T any](s *Server, w http.ResponseWriter, r *http.Request, errContext string, fetch func(platforms.Platform, context.Context, string) ([]T, error)) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := fetch(adapter, r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, errContext, err)
			return
		}
		if entries == nil {
			entries = []T{}
		}
		writeJSON(w, entries)
	})
}

func (s *Server) handleSessionAgents(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "fetching agent catalog", platforms.Platform.AgentCatalog)
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "fetching slash commands", platforms.Platform.SlashCommands)
}

func (s *Server) handleSessionModels(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		resp, err := adapter.SessionModels(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "fetching session models", err)
			return
		}
		if resp == nil {
			resp = &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
		}
		writeJSON(w, resp)
	})
}

func (s *Server) handleSessionPermissions(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		entries, err := adapter.ListPermissions(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "listing permissions", err)
			return
		}
		if entries == nil {
			entries = []platforms.LivePrompt{}
		}
		// Kick off auto-approve for any pending permissions resurrected
		// via REST. Without this, prompts that exist before the SSE
		// stream connects (page reload, navigation to a session that
		// already has a pending permission) would never trigger the
		// judge, leaving the UI stuck on the prompt indefinitely.
		// ensureAutoApprove deduplicates against the SSE tee so we
		// don't double-judge a permission that arrives via both paths.
		if !isRemotePlatformID(string(adapter.ID())) {
			for _, entry := range entries {
				permissionID, _ := entry["id"].(string)
				permission, _ := entry["permission"].(string)
				if permissionID == "" || permission == "" {
					continue
				}
				patterns := extractPermissionPatterns(entry)
				metadata := extractPermissionMetadata(entry)
				s.aaSvc().Ensure(adapter.ID(), adapter, promptSessionID(entry, sessionID), permissionID, permission, patterns, metadata)
			}
		}
		writeJSON(w, entries)
	})
}

func promptSessionID(entry platforms.LivePrompt, fallback string) string {
	if sessionID, _ := entry["sessionID"].(string); sessionID != "" {
		return sessionID
	}
	return fallback
}

// extractPermissionPatterns reads the "patterns" array from a
// LivePrompt map, tolerating both []string (rare) and []interface{}
// (the default after json.Unmarshal into a generic map).
func extractPermissionPatterns(entry platforms.LivePrompt) []string {
	raw, ok := entry["patterns"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// extractPermissionMetadata reads the "metadata" object from a
// LivePrompt map. Returns nil when absent or not an object — the
// judge prompt formatter handles nil cleanly (no metadata block
// appears in the prompt).
func extractPermissionMetadata(entry platforms.LivePrompt) map[string]any {
	raw, ok := entry["metadata"]
	if !ok {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return nil
}

func (s *Server) handleSessionQuestions(w http.ResponseWriter, r *http.Request) {
	listViaAdapter(s, w, r, "listing questions", platforms.Platform.ListQuestions)
}

// handleSessionChanges aggregates every file-touching tool call in a
// session into a per-file changes summary. Adapters that don't support
// the operation are surfaced as a Supported=false payload
// rather than an HTTP error so the frontend has a single shape to render.
func (s *Server) handleSessionChanges(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		changes, err := adapter.SessionChanges(r.Context(), sessionID)
		zero := &platforms.SessionChanges{SessionID: sessionID, Files: []platforms.FileChange{}}
		if changes != nil && changes.Files == nil {
			changes.Files = []platforms.FileChange{}
		}
		writeWithUnsupportedFallback(w, "fetching session changes", changes, err, zero)
	})
}

// writeWithUnsupportedFallback writes the adapter result as JSON. On
// ErrUnsupported (or a nil result) it substitutes `zero` instead, so
// platforms that don't implement the operation return a stable empty
// shape rather than an HTTP error. Any other error goes through
// writePlatformError.
func writeWithUnsupportedFallback[T any](w http.ResponseWriter, desc string, result *T, err error, zero *T) {
	if err != nil {
		if errors.Is(err, platforms.ErrUnsupported) {
			writeJSON(w, zero)
			return
		}
		writePlatformError(w, desc, err)
		return
	}
	if result == nil {
		writeJSON(w, zero)
		return
	}
	writeJSON(w, result)
}

// handleSessionInfo returns the per-session info snapshot consumed by
// the right-hand "Session info" panel.
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		info, err := adapter.SessionInfo(r.Context(), sessionID)
		zero := &platforms.SessionInfo{SessionID: sessionID, MCPServers: []platforms.MCPServer{}, LSPServers: []platforms.LSPServer{}}
		if info != nil {
			if info.MCPServers == nil {
				info.MCPServers = []platforms.MCPServer{}
			}
			if info.LSPServers == nil {
				info.LSPServers = []platforms.LSPServer{}
			}
		}
		writeWithUnsupportedFallback(w, "fetching session info", info, err, zero)
	})
}
