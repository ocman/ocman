// Pure logic for the "near-bottom auto-scroll" companion that sits
// alongside @assistant-ui/react's viewport.
//
// Background: the library decides `isAtBottom` with a hardcoded 1px
// tolerance. That's too strict for our streaming chats — small layout
// shifts (composer growth, image/code-block reflow during streaming)
// routinely leave the user a handful of pixels above the actual
// bottom. Once that happens, the library stops auto-scrolling and the
// user has to click "Scroll to bottom" manually.
//
// The companion hook (`useStickyBottom`) uses these helpers to relax
// that tolerance to ~80px while preserving "I scrolled up on purpose"
// intent. The hook owns the DOM/observer wiring; this module owns the
// decision logic so it can be unit-tested without a browser.

/**
 * How close to the bottom the viewport must be (in pixels) for us to
 * consider the user "at the bottom" and pull them along when new
 * content arrives.
 *
 * 80px corresponds to roughly 4-5 lines of body text at our default
 * font size — enough slack to absorb composer-resize and
 * streaming-induced layout shifts without overriding a deliberate
 * scroll-up.
 */
export const NEAR_BOTTOM_THRESHOLD_PX = 80;

export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

/**
 * Returns true when the viewport is within `threshold` pixels of the
 * bottom (or when the content is too short to scroll at all).
 */
export function isNearBottom(
  metrics: ScrollMetrics,
  threshold: number = NEAR_BOTTOM_THRESHOLD_PX,
): boolean {
  const { scrollTop, scrollHeight, clientHeight } = metrics;
  if (scrollHeight <= clientHeight) return true;
  const distanceFromBottom = scrollHeight - scrollTop - clientHeight;
  return distanceFromBottom <= threshold;
}

export type StickyEventKind = 'content' | 'user-scroll';

export interface StickyDecisionInput {
  /** Whether the viewport is currently within the near-bottom band. */
  isNear: boolean;
  /**
   * What triggered the decision:
   *  - 'content': DOM grew (new message, streaming chunk, image loaded)
   *  - 'user-scroll': the user scrolled the viewport
   */
  kind: StickyEventKind;
  /**
   * The sticky state *before* this event. Only used for 'content' events.
   *
   * When `true`, content growth keeps sticky engaged and scrolls to the
   * bottom even if `isNear` is momentarily false — this handles the race
   * where new DOM is appended but the browser hasn't yet propagated the
   * scroll position, causing a false "not near bottom" reading.
   *
   * Defaults to `false` when omitted (preserves old behaviour for callers
   * that don't track sticky state).
   */
  currentSticky?: boolean;
}

export interface StickyDecision {
  /** Sticky state after this event. */
  nextSticky: boolean;
  /**
   * Whether the caller should programmatically scroll to the bottom.
   * Always false for 'user-scroll' events to avoid feedback loops.
   */
  scroll: boolean;
}

/**
 * State-machine for the sticky-bottom hook.
 *
 * Rules:
 *  1. Content growth while near-bottom: stay/become sticky and scroll.
 *  2. Content growth while NOT near-bottom AND already sticky: stay
 *     sticky and scroll. This handles the common race where new DOM
 *     is appended (scrollHeight grows) but the browser hasn't yet
 *     propagated the updated scroll offset, so `isNear` transiently
 *     reads false even though the user never moved. Without this
 *     rule, a single bad rAF tick disengages sticky and the chat
 *     stops following new messages silently.
 *  3. Content growth while NOT near-bottom AND NOT sticky: do nothing —
 *     the user is deliberately reading older content.
 *  4. User scrolls into the near-bottom band: re-engage sticky (so
 *     subsequent content events resume pulling) but do NOT scroll
 *     (the user is already where they want to be).
 *  5. User scrolls out of the near-bottom band: disengage sticky.
 *     Programmatic scrolling here would fight the user.
 */
export function decideStickyAction({ isNear, kind, currentSticky = false }: StickyDecisionInput): StickyDecision {
  if (kind === 'content') {
    if (isNear || currentSticky) return { nextSticky: true, scroll: true };
    // Content grew while the user was deliberately reading older messages
    // (sticky was already off and they're not near the bottom) — leave
    // them alone. Sticky stays false so a future user-scroll back into
    // the near-bottom band re-engages it cleanly.
    return { nextSticky: false, scroll: false };
  }
  // kind === 'user-scroll' — never scroll programmatically (would fight
  // the user). Re-engage sticky if the user landed in the near-bottom
  // band; disengage otherwise.
  return { nextSticky: isNear, scroll: false };
}
