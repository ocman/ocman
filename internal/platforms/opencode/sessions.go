package opencode

import (
	"context"
	"slices"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// Sessions returns sessions filtered by directory and updated-after timestamp.
// Live permission/question prompts bypass the time window until resolved.
func (a *Adapter) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if a.db == nil {
		return nil, nil
	}
	dbPhase := srvtiming.Begin(ctx, "db_get_sessions")
	// Prompt flags come from live events, so apply the time window after enrichment.
	sessions, err := getSessionsCached(ctx, a.db, dir, 0)
	dbPhase.End()
	if err != nil {
		return nil, err
	}
	// The cached slice is shared across concurrent readers; the
	// per-session overlay below mutates entries by index, so we
	// need our own slice. A shallow copy is enough — Session is a
	// value type with no pointer-shared mutable state we'd care
	// about here.
	sessions = append([]db.Session(nil), sessions...)
	// Discover live instances for connection flags. Pending prompts come
	// from the global event watcher, so session listing never fans out to
	// every instance's /permission and /question endpoints.
	portsPhase := srvtiming.Begin(ctx, "lsof_ports")
	ports := discoverOpenCodePorts()
	portsPhase.End()

	// Settle every status against the live turn signal before anything
	// else reads it, then drop the children that turn out to be idle.
	resolvedPorts := make(map[string]string)
	for i := range sessions {
		directory := sessions[i].Directory
		port, ok := resolvedPorts[directory]
		if !ok {
			port = portForDirectory(ports, directory)
			resolvedPorts[directory] = port
		}
		sessions[i].Status = a.settleStatusOnPort(sessions[i].ID, port, sessions[i].Status)
		sessions[i].Notice = a.sessionNoticeOnPort(sessions[i].ID, port)
		sessions[i].LiveConnection = port != ""
	}
	sessions = db.FilterInactiveChildren(sessions)

	pendingPerms, pendingQuestions := a.prompts.pendingSessionIDs()

	// OpenCode emits subagent prompts with the subagent's session ID,
	// not the parent's. The listing only contains parent sessions
	// (subagents are filtered out by SQL), so we resolve each prompted
	// subagent to its parent and apply the flag there. Parent prompts
	// pass through unchanged (their id maps to themselves).
	bubblePhase := srvtiming.Begin(ctx, "bubble_parents")
	pendingPerms = bubbleUpPromptsToParent(ctx, pendingPerms, a.db)
	pendingQuestions = bubbleUpPromptsToParent(ctx, pendingQuestions, a.db)
	bubblePhase.End()

	for i := range sessions {
		sessions[i].Platform = string(PlatformID)
		if pendingPerms[sessions[i].ID] {
			sessions[i].PendingPermission = true
		}
		if pendingQuestions[sessions[i].ID] {
			sessions[i].PendingQuestion = true
		}
	}
	return slices.DeleteFunc(sessions, func(session db.Session) bool {
		return since > 0 && session.TimeUpdated < since && !session.PendingPermission && !session.PendingQuestion
	}), nil
}
