import { describe, it, expect } from 'vitest';
import { isNearBottom, decideStickyAction, NEAR_BOTTOM_THRESHOLD_PX } from './stickyBottom';

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

describe('decideStickyAction', () => {
  // The hook drives two outputs from each scroll/mutation event:
  //   - nextSticky: should the hook keep pulling the user to the bottom?
  //   - scroll: should we programmatically scrollTo the bottom now?
  //
  // For content events the decision also depends on `currentSticky`: when
  // the hook was already following the tail, a transient "not near bottom"
  // reading (caused by scrollHeight growing before scrollTop updates) must
  // not disengage sticky.

  it('engages sticky on content growth when the user is near the bottom', () => {
    expect(
      decideStickyAction({ isNear: true, kind: 'content' }),
    ).toEqual({ nextSticky: true, scroll: true });
  });

  it('does not engage sticky on content growth when the user has scrolled up and was not sticky', () => {
    expect(
      decideStickyAction({ isNear: false, kind: 'content', currentSticky: false }),
    ).toEqual({ nextSticky: false, scroll: false });
  });

  it('stays sticky and scrolls on content growth when sticky was true but isNear is momentarily false (race condition fix)', () => {
    // This is the core bug fix: when new DOM is appended, scrollHeight
    // grows before the browser updates scrollTop. The rAF tick may see
    // isNear === false even though the user never scrolled up. Without
    // passing currentSticky, decideStickyAction would disengage the tail
    // and the chat would stop following new messages silently.
    expect(
      decideStickyAction({ isNear: false, kind: 'content', currentSticky: true }),
    ).toEqual({ nextSticky: true, scroll: true });
  });

  it('defaults currentSticky to false when omitted (backward-compatible)', () => {
    // Callers that don't pass currentSticky behave as they did before.
    expect(
      decideStickyAction({ isNear: false, kind: 'content' }),
    ).toEqual({ nextSticky: false, scroll: false });
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
