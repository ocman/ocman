import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import {
  decideStickyAction,
  isNearBottom,
  NEAR_BOTTOM_THRESHOLD_PX,
} from './stickyBottom';

interface UseStickyBottomOptions {
  /**
   * Pixels from the bottom at which the user is still considered
   * "at the bottom". Defaults to `NEAR_BOTTOM_THRESHOLD_PX` (80px).
   */
  threshold?: number;
}

/**
 * Companion hook that runs alongside `@assistant-ui/react`'s
 * `<ThreadPrimitive.Viewport autoScroll>`.
 *
 * The library considers the user "at the bottom" only when within 1px
 * of `scrollHeight - clientHeight`. That is too strict for streaming
 * chats: tiny layout shifts (composer textarea growing, late-loading
 * images, code-block re-rendering) routinely leave the user a handful
 * of pixels above the bottom, after which the library stops
 * auto-scrolling and the conversation appears to "lose" the bottom.
 *
 * This hook relaxes the tolerance to ~80px:
 *   - On DOM growth: if the viewport is within `threshold` pixels of
 *     the bottom, scroll to the bottom.
 *   - On user scroll into the near-bottom band: re-engage sticky-mode
 *     (so subsequent content events resume pulling) without scrolling.
 *   - On user scroll out of the near-bottom band: disengage. The user
 *     is reading older messages and we must not chase them.
 *
 * The hook is purely additive — it never disables the library's own
 * auto-scroll, only fills in the band the library refuses to handle.
 */
export function useStickyBottom(
  viewportRef: RefObject<HTMLElement | null>,
  { threshold = NEAR_BOTTOM_THRESHOLD_PX }: UseStickyBottomOptions = {},
): void {
  // Track whether we're currently in "follow the bottom" mode. Lives
  // in a ref (not state) because the scroll/observer handlers fire
  // outside React's render cycle and mutating state would queue
  // unnecessary re-renders.
  const stickyRef = useRef(true);
  // Set to true just before a programmatic scrollTo so the next
  // 'scroll' event (which scrollTo will fire) is ignored. Without
  // this, a content-driven scrollToBottom would synthesise a fake
  // "user scrolled" event at the new (already-at-bottom) position
  // and the state machine would briefly toggle through an
  // intermediate state.
  const suppressScrollEventRef = useRef(false);

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;

    const metrics = () => ({
      scrollTop: el.scrollTop,
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    });

    const scrollToBottom = () => {
      // 'auto' (instant) — `behavior: 'smooth'` would visibly chase
      // streaming chunks and disorient the user. The library uses
      // 'instant' for the same reason on resize.
      suppressScrollEventRef.current = true;
      el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
    };

    const onUserScroll = () => {
      if (suppressScrollEventRef.current) {
        suppressScrollEventRef.current = false;
        return;
      }
      const { nextSticky } = decideStickyAction({
        isNear: isNearBottom(metrics(), threshold),
        kind: 'user-scroll',
      });
      stickyRef.current = nextSticky;
    };

    const onContentChange = () => {
      const { nextSticky, scroll } = decideStickyAction({
        isNear: isNearBottom(metrics(), threshold),
        kind: 'content',
      });
      stickyRef.current = nextSticky;
      if (scroll) scrollToBottom();
    };

    el.addEventListener('scroll', onUserScroll, { passive: true });

    // Observe both DOM mutations (new messages, streaming chunks) and
    // size changes (images decoding, code blocks reflowing, composer
    // resizing). We mirror the library's own observation surface so
    // we react to the same events at the same time — just with a
    // looser at-bottom check.
    const resizeObserver = new ResizeObserver(onContentChange);
    resizeObserver.observe(el);

    const mutationObserver = new MutationObserver((mutations) => {
      // Skip pure style attribute changes — the library's
      // ThreadViewportSlack writes inline styles in response to
      // viewport changes, and reacting to those would create a
      // feedback loop. Same filter the library uses internally.
      const hasRelevantMutation = mutations.some(
        (m) => m.type !== 'attributes' || m.attributeName !== 'style',
      );
      if (hasRelevantMutation) onContentChange();
    });
    mutationObserver.observe(el, {
      childList: true,
      subtree: true,
      attributes: true,
      characterData: true,
    });

    return () => {
      el.removeEventListener('scroll', onUserScroll);
      resizeObserver.disconnect();
      mutationObserver.disconnect();
    };
    // viewportRef is stable across renders; threshold is a number so
    // re-running on change is cheap and correct.
  }, [viewportRef, threshold]);
}
