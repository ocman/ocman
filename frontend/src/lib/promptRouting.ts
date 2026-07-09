// Helpers for routing OpenCode prompt events (permission / question)
// from a parent session and any of its subagents to the same UI on
// the parent's session page.
//
// Background:
//   When OpenCode spawns a subagent (via the Task tool), that subagent
//   runs as a separate session with its own session ID. If the subagent
//   needs a permission/question, OpenCode emits the prompt with the
//   subagent's session ID — not the parent's.
//
//   ocman's listing view hides subagent sessions, so the user can only
//   reach the prompt through the parent session page. To make the prompt
//   visible there, every session-ID-based filter on the parent page must
//   accept events whose session ID belongs to a known subagent.
//
//   The Set of known subagent IDs is derived from the parent session's
//   parts (Task tool calls reference the spawned subagent ID).

/**
 * Returns true if an SSE event or pending-prompt entry with the given
 * `eventSessionId` is relevant to the parent session page identified
 * by `pageSessionId`. An event is relevant when:
 *
 *   - it has no session ID (legacy / parent-scoped event), or
 *   - its session ID matches the page's session, or
 *   - its session ID matches one of the parent's known subagents.
 *
 * Pure function so it can be unit-tested without touching React.
 */
export function isSessionRelevant(
  eventSessionId: string | undefined | null,
  pageSessionId: string,
  subagentIds: ReadonlySet<string>,
): boolean {
  if (!eventSessionId) return true;
  if (eventSessionId === pageSessionId) return true;
  return subagentIds.has(eventSessionId);
}

/**
 * Collect the IDs of every session in `sessions` whose `parentID`
 * matches `pageSessionId`. These are ocman's own MCP/worktree children:
 * the backend overlays the state.db child_sessions link onto each
 * session's `parentID`, but — unlike OpenCode Task subagents — nothing
 * in the parent's message parts references them, so they never appear
 * in the Task-derived subagent set. Merging this set into the prompt
 * relevance check makes an MCP child's permission/question prompt
 * surface on the parent page (regression from #268, where worktree
 * sessions moved onto the shared project instance and lost their
 * OpenCode parent_id).
 *
 * Pure so it can be unit-tested without React.
 */
export function mcpChildIdsOf(
  pageSessionId: string | undefined | null,
  sessions: ReadonlyArray<{ id: string; parentID?: string }>,
): Set<string> {
  const out = new Set<string>();
  if (!pageSessionId) return out;
  for (const s of sessions) {
    if (s.parentID && s.parentID === pageSessionId) out.add(s.id);
  }
  return out;
}
