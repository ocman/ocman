/**
 * Decide whether an assistant message should render at all.
 *
 * Empty assistant messages (no text, no tool calls, no images) are
 * normally skipped. The one exception is the live turn-summary anchor:
 * OpenCode can create/update the trailing assistant row before any
 * content has streamed in, and that row owns the turn summary line.
 * Skipping it would make the turn line flicker — disappearing between
 * SSE deltas and reappearing once content lands. Keeping the live anchor
 * mounted (even while empty) keeps the turn line continuously visible
 * while data streams in.
 *
 * Empty *non-anchor* messages are still skipped: they carry the turn
 * aggregate only so ownership can move between messages without the line
 * blanking out, and they must not render an empty container.
 */
export function shouldRenderAssistantMessage(
  hasContent: boolean,
  isLiveSummaryAnchor: boolean,
): boolean {
  return hasContent || isLiveSummaryAnchor;
}
