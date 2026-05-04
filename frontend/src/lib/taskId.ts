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
 *   1. `state.input.task_id` — set when resuming an existing task.
 *   2. `task_id: ses_…` text inside `state.output` (OpenCode appends
 *      this line to the streamed tool output).
 *   3. `state.output.task_id` when the output is a JSON object.
 *   4. `state.metadata.{sessionId, taskId, task_id}` for adapters
 *      that surface the reference outside the streamed text.
 *
 * Returns the empty string when no strategy yields a non-empty id —
 * callers treat the empty string as "not yet known".
 */
export function extractTaskId(state: Record<string, unknown> | undefined | null): string {
  if (!state) return '';

  // 1. input.task_id (highest priority — set immediately for resumes)
  const inp = state.input as Record<string, unknown> | undefined;
  if (inp && typeof inp.task_id === 'string' && inp.task_id) return inp.task_id;

  // 2. regex on the output text
  const output = state.output;
  if (typeof output === 'string') {
    const match = output.match(/task_id:\s*(ses_[^\s)]+)/);
    if (match) return match[1];
  }

  // 3. output.task_id when output is a structured object
  if (output && typeof output === 'object' && !Array.isArray(output)) {
    const out = output as Record<string, unknown>;
    if (typeof out.task_id === 'string' && out.task_id) return out.task_id;
  }

  // 4. metadata fields
  const meta = state.metadata as Record<string, unknown> | undefined;
  if (meta) {
    if (typeof meta.sessionId === 'string' && meta.sessionId) return meta.sessionId;
    if (typeof meta.taskId === 'string' && meta.taskId) return meta.taskId;
    if (typeof meta.task_id === 'string' && meta.task_id) return meta.task_id;
  }

  return '';
}
