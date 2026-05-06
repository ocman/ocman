/**
 * Tool names that produce a subagent / task call. The frontend has to
 * recognise both the original (`task` / `Task`) and the MCP-prefixed
 * variants (`mcp_task` / `mcp_Task`) so renaming the upstream tool
 * does not break the live preview / subagent navigation.
 */
export const TASK_TOOL_NAMES: ReadonlySet<string> = new Set([
  'task',
  'mcp_task',
  'Task',
  'mcp_Task',
]);

/**
 * Returns true when the given tool name represents a subagent task
 * call. Centralised so the matching list lives in one place.
 */
export function isTaskTool(toolName: string | undefined | null): boolean {
  return !!toolName && TASK_TOOL_NAMES.has(toolName);
}

/**
 * Extract a subagent / task session ID from the `state` block of a
 * task tool part. Strategies, in priority order:
 *
 *   1. `task_id: ses_…` text inside `state.output` (OpenCode appends
 *      this line to the streamed tool output once the subagent has
 *      actually been spawned).
 *   2. `state.output.task_id` when the output is a JSON object.
 *   3. `state.input.task_id` — only used while no output has arrived
 *      yet. This is the resume hint the user / agent supplied; the
 *      server may fork a fresh subagent session id, in which case
 *      strategies 1 and 2 will report the live id and must win.
 *   4. `state.metadata.{sessionId, taskId, task_id}` for adapters
 *      that surface the reference outside the streamed text.
 *
 * **Why output wins over input**: the live id is what SSE events
 * carry and what the subagent click should navigate to. The input
 * id is just a request — when OpenCode forks a new sub-session for
 * a resume, the input id becomes stale. Pre-refactor the
 * `OcmanRuntimeProvider` already implemented this priority order
 * (regex on output overrode any prior `inp.task_id`); the original
 * Phase 1 extraction accidentally inverted it. See
 * `spec/frontend-refactor/review.md` (B4) for the audit trail.
 *
 * Returns the empty string when no strategy yields a non-empty id —
 * callers treat the empty string as "not yet known".
 */
export function extractTaskId(state: Record<string, unknown> | undefined | null): string {
  if (!state) return '';

  // 1. regex on the output text — the live id, written by the
  // server once the subagent has actually been spawned.
  const output = state.output;
  if (typeof output === 'string') {
    const match = output.match(/task_id:\s*(ses_[^\s)]+)/);
    if (match) return match[1];
  }

  // 2. output.task_id when output is a structured object
  if (output && typeof output === 'object' && !Array.isArray(output)) {
    const out = output as Record<string, unknown>;
    if (typeof out.task_id === 'string' && out.task_id) return out.task_id;
  }

  // 3. input.task_id — the resume hint. Only correct while the
  // output is still empty; if the server forks the resume, the
  // strategies above will deliver the new id.
  const inp = state.input as Record<string, unknown> | undefined;
  if (inp && typeof inp.task_id === 'string' && inp.task_id) return inp.task_id;

  // 4. metadata fields
  const meta = state.metadata as Record<string, unknown> | undefined;
  if (meta) {
    if (typeof meta.sessionId === 'string' && meta.sessionId) return meta.sessionId;
    if (typeof meta.taskId === 'string' && meta.taskId) return meta.taskId;
    if (typeof meta.task_id === 'string' && meta.task_id) return meta.task_id;
  }

  return '';
}
