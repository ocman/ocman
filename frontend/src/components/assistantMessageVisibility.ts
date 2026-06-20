/**
 * Decide whether an assistant message should render at all.
 *
 * Empty assistant messages (no text, no tool calls, no images) are
 * normally skipped. The one exception is while the turn is still live:
 * OpenCode can create/update the trailing assistant row before any
 * content has streamed in, and that row owns the turn summary line.
 * Skipping it would make the turn line flicker — disappearing between
 * SSE deltas and reappearing once content lands. Keeping a live, empty
 * message mounted keeps the turn line continuously visible while data
 * streams in.
 */
export function shouldRenderAssistantMessage(
  hasContent: boolean,
  turnIsLive: boolean,
): boolean {
  return hasContent || turnIsLive;
}
