package opencode

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Pending-prompt listing: permission and question prompts fetched
// from a running OpenCode instance, filtered to the requesting
// session (including its subagent children).

// ListPermissions returns pending permission prompts for the session's
// directory. Filters out prompts for other sessions — the frontend
// only cares about those it could act on.
func (a *Adapter) ListPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return a.listPrompts(ctx, sessionID, "/permission")
}

// ListQuestions returns pending question prompts for the session.
func (a *Adapter) ListQuestions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return a.listPrompts(ctx, sessionID, "/question")
}

func (a *Adapter) listPrompts(ctx context.Context, sessionID, path string) ([]platforms.LivePrompt, error) {
	port, _, err := a.resolvePortCtx(ctx, sessionID)
	if err != nil {
		return nil, nil
	}
	body, ok := getJSON(ctx, port, path)
	if !ok {
		return nil, nil
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil
	}
	// Subagent prompts carry the subagent's session ID, not the parent's.
	// Bubble them up so the parent session's UI can render and respond to
	// them — otherwise OpenCode stalls waiting on a prompt the user can't
	// see (the subagent sessions are hidden from the listing).
	//
	// We're already on the live path (resolvePort succeeded), so prefer
	// OpenCode's GET /session/:id/children over the read-only DB. This
	// removes a DB hit from RespondPermission's neighbour code and keeps
	// the live mutating path API-pure. Falls back to the DB on upstream
	// failure so prompts still bubble when, e.g., OpenCode briefly drops
	// the children endpoint (older versions, transient errors).
	subagentIDs := fetchSubagentSessionIDs(ctx, port, sessionID)
	if subagentIDs == nil && a.db != nil {
		subagentIDs, _ = a.db.GetSubagentSessionIDs(sessionID)
	}
	return filterPromptsForSession(raw, sessionID, subagentIDs), nil
}

// fetchSubagentSessionIDs calls GET /session/:id/children on the
// running OpenCode instance and returns the IDs of every direct child
// (subagent) session. Returns nil on any upstream failure so callers
// can fall back to the DB lookup — the result is best-effort UI
// plumbing, never a hard dependency.
//
// Routed through catalogCache: a parent session's children list
// changes only when a new subagent spawns, which is rare enough on
// the timescale of a single dashboard poll that the 30s TTL is
// fine. This also keeps the SSE-driven listPrompts polling cheap
// when multiple sessions on the same instance are in flight.
func fetchSubagentSessionIDs(ctx context.Context, port, sessionID string) []string {
	body, ok := getJSONCached(ctx, port, fmt.Sprintf("/session/%s/children", sessionID))
	if !ok {
		return nil
	}
	return parseSubagentChildIDs(body)
}

// parseSubagentChildIDs extracts the `id` field of every entry in
// OpenCode's GET /session/:id/children response. Permissive: ignores
// entries with an empty/missing id and returns nil for malformed
// payloads (so listPrompts's nil-check triggers the DB fallback
// rather than treating "broken upstream" as "no children").
func parseSubagentChildIDs(body []byte) []string {
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		if id, ok := entry["id"].(string); ok && id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// filterPromptsForSession returns the subset of OpenCode prompt entries
// (from /permission or /question) that belong to the given session or any
// of its subagents. Entries without a sessionID are kept as-is — older
// OpenCode versions emit parent-scoped prompts that way.
//
// Kept as a pure function so the inclusion logic is testable without
// spinning up an HTTP server or running OpenCode.
func filterPromptsForSession(raw []map[string]interface{}, sessionID string, subagentIDs []string) []platforms.LivePrompt {
	allowed := make(map[string]bool, 1+len(subagentIDs))
	allowed[sessionID] = true
	for _, id := range subagentIDs {
		allowed[id] = true
	}
	out := make([]platforms.LivePrompt, 0, len(raw))
	for _, r := range raw {
		sid, hasSID := r["sessionID"].(string)
		if hasSID && sid != "" && !allowed[sid] {
			continue
		}
		out = append(out, platforms.LivePrompt(r))
	}
	return out
}
