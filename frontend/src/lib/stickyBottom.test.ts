import { describe, it, expect } from 'vitest';
import {
  isNearBottom,
  decideStickyAction,
  distanceFromBottom,
  nextPillVisible,
  NEAR_BOTTOM_THRESHOLD_PX,
  PILL_SHOW_DISTANCE_PX,
} from './stickyBottom';

// `@assistant-ui/react`'s viewport considers the user "at the bottom"
// only when within 1px of `scrollHeight - clientHeight`. That breaks
// auto-scroll for streaming chats whenever the layout shifts a few
// pixels (composer growth, late-loading images, code blocks
// re-rendering during streaming): the user thinks they're at the
// bottom, but the library has already flipped `isAtBottom` to false
// and stopped pulling new content into view.
//
// `isNearBottom` + `decideStickyAction` form the pure core of a
// companion hook (`useStickyBottom`) that runs alongside the library
// and pulls the viewport down when the user is "near" the bottom
// (default 80px). The hook itself is wired in `AssistantThread`.

describe('isNearBottom', () => {
  it('returns true when scroll position is exactly at the bottom', () => {
    expect(isNearBottom({ scrollTop: 900, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
  });

  it('returns true when content fits without scrolling (scrollHeight <= clientHeight)', () => {
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 100, clientHeight: 200 })).toBe(true);
  });

  it('returns true when within the default 80px threshold', () => {
    // distance = scrollHeight - scrollTop - clientHeight = 1000 - 850 - 100 = 50
    expect(isNearBottom({ scrollTop: 850, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
  });

  it('returns true exactly at the threshold boundary', () => {
    // distance = 80
    expect(isNearBottom({ scrollTop: 820, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
  });

  it('returns false when just past the default threshold', () => {
    // distance = 81
    expect(isNearBottom({ scrollTop: 819, scrollHeight: 1000, clientHeight: 100 })).toBe(false);
  });

  it('returns false when far above the bottom', () => {
    expect(isNearBottom({ scrollTop: 100, scrollHeight: 5000, clientHeight: 500 })).toBe(false);
  });

  it('honours a custom threshold', () => {
    // distance = 150, threshold = 200 -> near
    expect(isNearBottom({ scrollTop: 750, scrollHeight: 1000, clientHeight: 100 }, 200)).toBe(true);
    // distance = 150, threshold = 100 -> not near
    expect(isNearBottom({ scrollTop: 750, scrollHeight: 1000, clientHeight: 100 }, 100)).toBe(false);
  });

  it('treats sub-pixel rounding (fractional scrollTop) as at-bottom', () => {
    // Browsers occasionally report scrollTop with a fractional pixel due to
    // device pixel ratio. distance = 1000 - 899.5 - 100 = 0.5
    expect(isNearBottom({ scrollTop: 899.5, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
  });

  it('exposes a sensible default threshold', () => {
    // Documenting the contract: any change to the constant is intentional
    // (it directly affects how forgiving auto-scroll feels).
    expect(NEAR_BOTTOM_THRESHOLD_PX).toBe(80);
  });
});

describe('distanceFromBottom', () => {
  it('measures the unseen content below the viewport', () => {
    expect(distanceFromBottom({ scrollTop: 100, scrollHeight: 2000, clientHeight: 1000 })).toBe(900);
  });

  it('is zero when the content fits without scrolling', () => {
    expect(distanceFromBottom({ scrollTop: 0, scrollHeight: 100, clientHeight: 200 })).toBe(0);
  });

  it('never reports a negative distance when the browser over-scrolls', () => {
    // Rubber-band / over-scroll can transiently report a scrollTop past
    // the maximum, which would otherwise yield a negative distance and
    // make the hysteresis comparisons behave oddly.
    expect(distanceFromBottom({ scrollTop: 1200, scrollHeight: 2000, clientHeight: 1000 })).toBe(0);
  });
});

describe('nextPillVisible', () => {
  // The band exists because a single threshold flickers: streaming
  // content jumps the scroll height by tens of pixels at a time.

  it('reveals the affordance only past the show distance', () => {
    expect(nextPillVisible(false, PILL_SHOW_DISTANCE_PX)).toBe(false);
    expect(nextPillVisible(false, PILL_SHOW_DISTANCE_PX + 1)).toBe(true);
  });

  it('hides it again only within the near-bottom threshold', () => {
    expect(nextPillVisible(true, NEAR_BOTTOM_THRESHOLD_PX + 1)).toBe(true);
    expect(nextPillVisible(true, NEAR_BOTTOM_THRESHOLD_PX)).toBe(false);
  });

  it('holds its current value inside the band, whichever way it was entered', () => {
    // This is the anti-flicker property: a size jump that lands anywhere
    // between the two thresholds changes nothing.
    for (const distance of [NEAR_BOTTOM_THRESHOLD_PX + 1, 150, PILL_SHOW_DISTANCE_PX]) {
      expect(nextPillVisible(false, distance)).toBe(false);
      expect(nextPillVisible(true, distance)).toBe(true);
    }
  });

  it('keeps the two thresholds far enough apart to absorb streaming noise', () => {
    expect(PILL_SHOW_DISTANCE_PX).toBeGreaterThan(NEAR_BOTTOM_THRESHOLD_PX * 2);
  });
});

describe('decideStickyAction', () => {
  // The hook drives two outputs from each scroll/mutation/gesture event:
  //   - nextSticky: should the hook keep pulling the user to the bottom?
  //   - scroll: should we programmatically scrollTo the bottom now?
  //
  // Sticky is a mode the user leaves by gesturing. Content events carry
  // the mode forward; they do not re-derive it from geometry.

  it('follows the tail on content growth while engaged, regardless of geometry', () => {
    // The reason `isNear` is not consulted: while following, growth
    // below the fold makes the viewport look far from the bottom on the
    // very tick that should scroll it there.
    for (const isNear of [true, false]) {
      expect(
        decideStickyAction({ isNear, kind: 'content', currentSticky: true }),
      ).toEqual({ nextSticky: true, scroll: true });
    }
  });

  it('leaves the viewport alone on content growth while disengaged, regardless of geometry', () => {
    // The mirror case, and the one that used to break: a shrink above
    // the fold (message trim, tool block collapsing, scroll anchoring)
    // moves scrollTop into the near-bottom band without the user
    // touching anything. Reading that as "they're back at the bottom"
    // re-engaged the follow and dragged them out of the history.
    for (const isNear of [true, false]) {
      expect(
        decideStickyAction({ isNear, kind: 'content', currentSticky: false }),
      ).toEqual({ nextSticky: false, scroll: false });
    }
  });

  it('defaults currentSticky to false when omitted', () => {
    expect(decideStickyAction({ kind: 'content' })).toEqual({ nextSticky: false, scroll: false });
  });

  it('disengages on a scroll gesture, whatever the state', () => {
    // A wheel/touch gesture is applied on the compositor thread frames
    // before its `scroll` event reaches JS. Acting on the gesture is
    // what closes the window in which a content tick could read stale
    // geometry and scroll the reader back down.
    for (const currentSticky of [true, false]) {
      for (const isNear of [true, false]) {
        expect(
          decideStickyAction({ isNear, kind: 'gesture', currentSticky }),
        ).toEqual({ nextSticky: false, scroll: false });
      }
    }
  });

  it('never scrolls on a gesture', () => {
    // Scrolling in response to the user scrolling is the feedback loop
    // this whole module exists to avoid.
    expect(decideStickyAction({ kind: 'gesture', currentSticky: true }).scroll).toBe(false);
  });

  it('disengages sticky when the user scrolls up past the threshold', () => {
    // A user-initiated scroll-up beyond the near-bottom band is the
    // explicit signal "I want to read older content; stop chasing the
    // bottom". This must NOT trigger another scroll.
    expect(
      decideStickyAction({ isNear: false, kind: 'user-scroll' }),
    ).toEqual({ nextSticky: false, scroll: false });
  });

  it('re-engages sticky when the user scrolls back into the near-bottom band', () => {
    // Scrolling back down to the bottom should restore the "follow new
    // messages" behaviour without requiring a click on the
    // ScrollToBottom button. We do NOT scroll on this event itself —
    // the user just put themselves where they want to be.
    expect(
      decideStickyAction({ isNear: true, kind: 'user-scroll' }),
    ).toEqual({ nextSticky: true, scroll: false });
  });

  it('never scrolls on a user-scroll event, even when it transitions sticky', () => {
    // Calling scrollTo in response to a scroll event would create a
    // feedback loop. Only content-growth events trigger programmatic
    // scrolling.
    const cases = [
      { isNear: true, kind: 'user-scroll' as const },
      { isNear: false, kind: 'user-scroll' as const },
    ];
    for (const c of cases) {
      expect(decideStickyAction(c).scroll).toBe(false);
    }
  });

  it('currentSticky has no effect on user-scroll events (user intent always wins)', () => {
    // currentSticky is only meaningful for content events. User-scroll
    // events always reflect explicit intent and must not be overridden.
    expect(
      decideStickyAction({ isNear: false, kind: 'user-scroll', currentSticky: true }),
    ).toEqual({ nextSticky: false, scroll: false });
    expect(
      decideStickyAction({ isNear: true, kind: 'user-scroll', currentSticky: false }),
    ).toEqual({ nextSticky: true, scroll: false });
  });
});
