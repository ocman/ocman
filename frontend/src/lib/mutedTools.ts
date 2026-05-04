/**
 * Tool names that render as a single muted line in the assistant
 * thread instead of getting their own card. These are the "boring"
 * lookups (read / grep / glob / webfetch) whose results are usually
 * verbose log noise the user does not want to scan unless it failed.
 *
 * Both the unprefixed names (used by Claude Code) and the
 * `mcp_`-prefixed names (used by OpenCode) are listed because the
 * frontend renders both platforms uniformly.
 */
export const MUTED_TOOL_NAMES: ReadonlySet<string> = new Set([
  '__read__',
  'read',
  'mcp_read',
  'grep',
  'mcp_grep',
  'glob',
  'mcp_glob',
  'webfetch',
  'mcp_webfetch',
  'mcp_Webfetch',
]);

/**
 * Superset of `MUTED_TOOL_NAMES` that adds `__skill__` — the synthetic
 * tool emitted by skill loads. The line-renderer collapses skill loads
 * along with the other muted entries; the per-card renderer does not.
 */
export const MUTED_LINE_TOOL_NAMES: ReadonlySet<string> = new Set([
  ...MUTED_TOOL_NAMES,
  '__skill__',
]);

/** Convenience predicate matching `MUTED_TOOL_NAMES`. */
export function isMutedTool(name: string | undefined | null): boolean {
  return !!name && MUTED_TOOL_NAMES.has(name);
}

/** Convenience predicate matching `MUTED_LINE_TOOL_NAMES`. */
export function isMutedLineTool(name: string | undefined | null): boolean {
  return !!name && MUTED_LINE_TOOL_NAMES.has(name);
}
