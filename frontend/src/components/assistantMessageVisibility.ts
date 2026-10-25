/**
 * Decide whether an assistant message should render at all.
 *
 * Empty assistant messages (no text, no tool calls, no images) are
 * normally skipped. The exception is the turn-summary anchor: OpenCode
 * can create the trailing assistant row before any content streams in,
 * and a turn can end on an empty step. That row owns the turn summary
 * line, so skipping it would hide the turn's metadata, both while it is
 * live and after it finishes.
 *
 * Empty *non-anchor* messages are still skipped: they carry the turn
 * aggregate only so ownership can move between messages without the line
 * blanking out, and they must not render an empty container.
 */
export function shouldRenderAssistantMessage(
  hasContent: boolean,
  isSummaryAnchor: boolean,
): boolean {
  return hasContent || isSummaryAnchor;
}
