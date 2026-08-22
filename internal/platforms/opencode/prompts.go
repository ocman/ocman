package opencode

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Pending-prompt listing: permission and question prompts fetched
// from a running OpenCode instance, filtered to the requesting
// session (including its subagent children).

// ListPermissions returns pending permission prompts for the session's
// directory. Filters out prompts for other sessions — the frontend
// only cares about those it could act on.
func (a *Adapter) ListPermissions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	return a.listObservedPrompts(ctx, "permission", sessionID), nil
}

// ListQuestions returns pending question prompts for the session.
func (a *Adapter) ListQuestions(ctx context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	if port, session, err := a.resolvePortCtx(ctx, sessionID); err == nil {
		directories := append([]string{session.Directory}, a.descendantDirectories(ctx, sessionID)...)
		for _, entry := range a.observedPromptEntries(ctx, "question", sessionID) {
			directories = append(directories, entry.directory)
		}
		if ok := <-a.startPromptReconciliation(ctx, port, uniqueStrings(directories), []string{"question"}, false); !ok {
			return nil, fmt.Errorf("refreshing pending questions: %w", platforms.ErrPlatformUnreachable)
		}
	}
	return a.listObservedPrompts(ctx, "question", sessionID), nil
}

func (a *Adapter) descendantDirectories(ctx context.Context, sessionID string) []string {
	if a.db == nil {
		return nil
	}
	mcpChildren := make(map[string][]string)
	if a.childLinks != nil {
		if parents, err := a.childLinks.ChildSessionParents(ctx); err == nil {
			for key, parentID := range parents {
				mcpChildren[parentID] = append(mcpChildren[parentID], key.SessionID)
			}
		}
	}
	ids := make(map[string]bool)
	queue := []string{sessionID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		children, _ := a.db.GetSubagentSessionIDs(ctx, parentID)
		children = append(children, mcpChildren[parentID]...)
		for _, childID := range children {
			if !ids[childID] {
				ids[childID] = true
				queue = append(queue, childID)
			}
		}
	}
	var directories []string
	for id := range ids {
		if session, err := a.db.GetSession(ctx, id); err == nil {
			directories = append(directories, session.Directory)
		}
	}
	return uniqueStrings(directories)
}

func (a *Adapter) listObservedPrompts(ctx context.Context, kind, sessionID string) []platforms.LivePrompt {
	entries := a.observedPromptEntries(ctx, kind, sessionID)
	out := make([]platforms.LivePrompt, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.prompt)
	}
	return out
}

func (a *Adapter) observedPromptEntries(ctx context.Context, kind, sessionID string) []livePromptEntry {
	entries := a.prompts.listEntries(kind)
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		prompt := entry.prompt
		ids = append(ids, promptString(prompt, "sessionID"))
	}
	parents := map[string]string{}
	if a.db != nil {
		parents, _ = a.db.GetSessionParentIDs(ctx, ids)
	}
	mcpParents := map[state.Key]string{}
	if a.childLinks != nil {
		mcpParents, _ = a.childLinks.ChildSessionParents(ctx)
	}

	out := make([]livePromptEntry, 0, len(entries))
	for _, entry := range entries {
		prompt := entry.prompt
		promptSessionID := promptString(prompt, "sessionID")
		nativeAncestor := parents[promptSessionID]
		if promptSessionID == sessionID || nativeAncestor == sessionID || mcpDescendsFrom(mcpParents, promptSessionID, sessionID) || mcpDescendsFrom(mcpParents, nativeAncestor, sessionID) {
			out = append(out, entry)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func mcpDescendsFrom(parents map[state.Key]string, childID, ancestorID string) bool {
	seen := make(map[string]bool)
	for childID != "" && !seen[childID] {
		seen[childID] = true
		parentID := parents[state.Key{Platform: string(PlatformID), SessionID: childID}]
		if parentID == ancestorID {
			return true
		}
		childID = parentID
	}
	return false
}
