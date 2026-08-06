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
 * How far the viewport must be from the bottom before the
 * scroll-to-bottom affordance appears.
 *
 * Deliberately well past `NEAR_BOTTOM_THRESHOLD_PX`: together the two
 * form a hysteresis band (show past 240px, hide within 80px, hold
 * between). Streaming content routinely jumps the scroll height by tens
 * of pixels — a truncated bash block resolving, a code fence
 * highlighting — and a single threshold turns each of those into a
 * flash of the button. A 160px-wide band cannot be crossed by that
 * noise, so the visibility is stable without a timer.
 */
export const PILL_SHOW_DISTANCE_PX = 240;

/** Pixels of unseen content below the viewport. */
export function distanceFromBottom({ scrollTop, scrollHeight, clientHeight }: ScrollMetrics): number {
  if (scrollHeight <= clientHeight) return 0;
  return Math.max(0, scrollHeight - scrollTop - clientHeight);
}

/**
 * Returns true when the viewport is within `threshold` pixels of the
 * bottom (or when the content is too short to scroll at all).
 */
export function isNearBottom(
  metrics: ScrollMetrics,
  threshold: number = NEAR_BOTTOM_THRESHOLD_PX,
): boolean {
  const { scrollHeight, clientHeight } = metrics;
  if (scrollHeight <= clientHeight) return true;
  return distanceFromBottom(metrics) <= threshold;
}

/**
 * Whether the scroll-to-bottom affordance should be visible, given
 * whether it is visible now. Hysteresis: crossing `showAt` reveals it,
 * and only coming back within `hideAt` hides it again.
 *
 * This replaces a 400ms CSS show-delay that existed to swallow exactly
 * the flicker the band now prevents outright — a delay hides the
 * symptom but also makes a deliberate scroll-up feel unresponsive.
 */
export function nextPillVisible(
  visible: boolean,
  distance: number,
  hideAt: number = NEAR_BOTTOM_THRESHOLD_PX,
  showAt: number = PILL_SHOW_DISTANCE_PX,
): boolean {
  return visible ? distance > hideAt : distance > showAt;
}

export type StickyEventKind = 'content' | 'user-scroll' | 'gesture';

export interface StickyDecisionInput {
  /**
   * Whether the viewport is currently within the near-bottom band.
   * Only read for 'user-scroll' — see `decideStickyAction`.
   */
  isNear?: boolean;
  /**
   * What triggered the decision:
   *  - 'content': DOM grew (new message, streaming chunk, image loaded)
   *  - 'user-scroll': the viewport's scroll offset changed
   *  - 'gesture': the user physically moved the viewport (wheel, touch
   *    drag, scrollbar press)
   */
  kind: StickyEventKind;
  /**
   * The sticky state *before* this event.
   *
   * For 'content' this is the whole decision: we follow the tail because
   * the user has not gestured away from it, not because of where the
   * viewport currently sits.
   *
   * Defaults to `false` when omitted.
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
 * Sticky is a mode the user leaves by *gesturing*, not a property of
 * where the viewport happens to sit. That distinction is the whole
 * design:
 *
 *  - 'gesture' (wheel, touch drag, scrollbar press): disengage, always
 *    and immediately. A wheel scroll is applied on the compositor
 *    thread frames before its `scroll` event reaches JS, so a content
 *    tick landing in that window used to read the *stale* pre-scroll
 *    geometry, conclude "still near the bottom", and yank the reader
 *    back down mid-stream. The gesture arrives first, so acting on it
 *    closes that window instead of trying to out-guess it.
 *
 *  - 'content' (new message, streaming chunk, image decoded): follow
 *    the tail iff we were already following. Deliberately does NOT
 *    consult `isNear`. Geometry lies here in both directions: growth
 *    below the fold makes a following viewport look far from the
 *    bottom, while a shrink above it (message trim, a tool block
 *    collapsing, browser scroll anchoring) lowers `scrollTop` without
 *    the user touching anything. Reading either as intent is what made
 *    the follow drop silently. Not reading geometry at all also means
 *    a content tick costs no forced layout.
 *
 *  - 'user-scroll' (the offset actually changed): re-engage when the
 *    viewport lands inside the near-bottom band, disengage when it
 *    doesn't. This is what re-arms follow after the user scrolls back
 *    down, and what disengages on a programmatic jump to an older
 *    message. Never scrolls — that would fight the user.
 */
export function decideStickyAction({
  isNear = false,
  kind,
  currentSticky = false,
}: StickyDecisionInput): StickyDecision {
  if (kind === 'gesture') return { nextSticky: false, scroll: false };
  if (kind === 'content') return { nextSticky: currentSticky, scroll: currentSticky };
  return { nextSticky: isNear, scroll: false };
}
