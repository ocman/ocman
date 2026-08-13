import { useCallback, useEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';
import {
  decideStickyAction,
  distanceFromBottom,
  isNearBottom,
  nextPillVisible,
  NEAR_BOTTOM_THRESHOLD_PX,
} from './stickyBottom';

export interface StickyBottomState {
  /**
   * Whether to offer a "scroll to bottom" affordance. Driven by a
   * hysteresis band, so it does not flicker while content streams.
   */
  showScrollToBottom: boolean;
  /** Jump to the tail and resume following it. */
  scrollToBottom: () => void;
}

interface UseStickyBottomOptions {
  /**
   * Pixels from the bottom at which the user is still considered
   * "at the bottom". Defaults to `NEAR_BOTTOM_THRESHOLD_PX` (80px).
   */
  threshold?: number;
  /**
   * Selector for subtrees whose scroll gestures are not about the
   * conversation. Gestures originating inside a match are ignored.
   *
   * This exists because the composer is rendered *inside* the scroll
   * container: without it, clicking into the textarea to type — or
   * flicking a long draft on a touch screen — bubbles a gesture up to
   * the viewport and stops the reply being followed.
   */
  ignoreGesturesWithin?: string;
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
 * This hook relaxes the tolerance to ~80px and, more importantly,
 * treats following the tail as a *mode the user leaves by gesturing*
 * rather than something re-derived from geometry on every tick:
 *   - On a scroll gesture (wheel, touch drag, scrollbar press):
 *     disengage immediately, before the resulting `scroll` event.
 *   - On DOM growth: scroll to the bottom iff still engaged. No
 *     geometry is read, so a shrink above the fold or growth below it
 *     can no longer be mistaken for intent.
 *   - On the offset landing back inside the near-bottom band: re-engage
 *     without scrolling (the user is already where they want to be).
 *
 * The viewport's own `autoScroll` is disabled by the caller (its 1px
 * tolerance races streaming DOM growth); this hook owns auto-scroll.
 */
export function useStickyBottom(
  viewportRef: RefObject<HTMLElement | null>,
  { threshold = NEAR_BOTTOM_THRESHOLD_PX, ignoreGesturesWithin }: UseStickyBottomOptions = {},
): StickyBottomState {
  // Track whether we're currently in "follow the bottom" mode. Lives
  // in a ref (not state) because the scroll/observer handlers fire
  // outside React's render cycle and mutating state would queue
  // unnecessary re-renders.
  const stickyRef = useRef(true);
  // The affordance's visibility, by contrast, has to be state — it is
  // rendered. Its hysteresis band means it changes rarely, so this
  // costs a re-render only when the user genuinely crosses the band.
  const [showScrollToBottom, setShowScrollToBottom] = useState(false);

  // Imperative jump for the affordance. Re-engages follow: clicking
  // "scroll to bottom" is as explicit as intent gets.
  const scrollToBottom = useCallback(() => {
    const el = viewportRef.current;
    if (!el) return;
    stickyRef.current = true;
    setShowScrollToBottom(false);
    el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
  }, [viewportRef]);

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;

    const metrics = () => ({
      scrollTop: el.scrollTop,
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    });

    // Initialise sticky state from the actual scroll position instead of
    // blindly assuming the user starts at the bottom. If the hook mounts
    // while the user is far up (e.g. an already-scrolled conversation), we
    // must not drag them back down on the first content event.
    stickyRef.current = isNearBottom(metrics(), threshold);

    // Follow-the-tail scroll. Distinct from the exported `scrollToBottom`
    // above: this one is the automatic path and must not touch sticky
    // state (it is only ever called while already engaged).
    //
    // 'auto' (instant) — `behavior: 'smooth'` would visibly chase
    // streaming chunks and disorient the user. The library uses
    // 'instant' for the same reason on resize.
    const scrollToTail = () => {
      el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
    };

    const onScroll = () => {
      const m = metrics();
      // A scroll event has no source: browser anchoring, content shrinking,
      // focus, and user input all produce the same event. Only an explicit
      // upward gesture may leave follow mode; landing at the tail re-arms it.
      if (isNearBottom(m, threshold)) stickyRef.current = true;
      const distance = distanceFromBottom(m);
      setShowScrollToBottom((visible) => nextPillVisible(visible, distance));
    };

    // Physical scroll gestures disengage immediately, without waiting
    // for the `scroll` event they will produce.
    //
    // A wheel or touch drag is applied on the compositor thread frames
    // before its `scroll` event is dispatched to JS. A content tick
    // landing inside that window reads pre-gesture geometry, concludes
    // the viewport is still near the bottom, and scrolls the reader back
    // down — the classic "it keeps yanking me to the bottom while I'm
    // trying to read" report. The gesture event fires first, so the
    // cheapest correct fix is to believe it.
    //
    // Typing in the composer must not leave follow mode. Navigation keys
    // are handled separately because their resulting scroll event has no
    // source information.
    //
    const isIgnoredGesture = (event: Event) => {
      if (ignoreGesturesWithin) {
        const target = event.target;
        if (target instanceof Element && target.closest(ignoreGesturesWithin)) return true;
      }
      return false;
    };

    const disengage = () => {
      const { nextSticky } = decideStickyAction({ kind: 'gesture' });
      stickyRef.current = nextSticky;
    };

    // A click is not a scroll. Wheel/touch input only leaves follow mode when
    // it moves toward older messages; scrolling back down is re-armed by the
    // resulting scroll event once it reaches the tail.
    const onWheel = (event: WheelEvent) => {
      if (!isIgnoredGesture(event) && event.deltaY < 0) disengage();
    };
    const onTouchMove = (event: TouchEvent) => {
      const touch = event.touches?.[0];
      if (!isIgnoredGesture(event) && touch && lastTouchY !== null && touch.clientY > lastTouchY) {
        disengage();
      }
      lastTouchY = touch?.clientY ?? null;
    };
    let lastTouchY: number | null = null;
    const onTouchStart = (event: TouchEvent) => {
      lastTouchY = isIgnoredGesture(event) ? null : (event.touches?.[0]?.clientY ?? null);
    };
    const onPointerDown = (event: PointerEvent) => {
      const rect = el.getBoundingClientRect();
      if (!isIgnoredGesture(event) && el.offsetWidth > el.clientWidth && event.clientX >= rect.left + el.clientWidth) {
        disengage();
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      const scrollsUp = ['ArrowUp', 'PageUp', 'Home'].includes(event.code)
        || (event.code === 'Space' && event.shiftKey);
      if (!isIgnoredGesture(event) && scrollsUp) disengage();
    };

    // rAF-coalesced content-change handler.
    //
    // The Mutation/ResizeObservers below fire on every DOM mutation
    // inside the viewport — and during streaming (~30 deltas/sec)
    // that means dozens of synchronous handler invocations per
    // second. Each invocation reads `scrollTop` / `scrollHeight` /
    // `clientHeight`, which forces the browser to flush layout. A
    // forced layout takes a few ms each but it BLOCKS the main
    // thread, including click event dispatch — which manifests as
    // "navigation only happens when the LLM finishes a complete
    // block" because that's when streaming tokens pause briefly and
    // the click handler finally gets to run.
    //
    // Coalesce all mutations within a frame into one layout read by
    // queuing the work into a rAF. A single rAF callback per
    // animation frame is the canonical pattern for "react to DOM
    // changes without spamming layout" — the browser already
    // batches paint work at this granularity.
    let rafHandle: number | null = null;
    const scheduleContentCheck = () => {
      if (rafHandle !== null) return;
      rafHandle = requestAnimationFrame(() => {
        rafHandle = null;
        // No geometry read here on purpose: whether we follow is decided
        // by whether the user has gestured away, not by where the
        // viewport sits (see decideStickyAction). That also makes the
        // common case — a streaming chunk while following — cost zero
        // forced layouts beyond the scroll itself.
        const { nextSticky, scroll } = decideStickyAction({
          kind: 'content',
          currentSticky: stickyRef.current,
        });
        stickyRef.current = nextSticky;
        if (scroll) {
          scrollToTail();
          return;
        }
        // Disengaged: content growing below the fold is exactly when the
        // affordance needs to appear, and no scroll event will fire to
        // tell us. Reading geometry here is affordable precisely because
        // we are *not* streaming-and-following — that hot path above
        // still forces no layout.
        const distance = distanceFromBottom(metrics());
        setShowScrollToBottom((visible) => nextPillVisible(visible, distance));
      });
    };

    el.addEventListener('scroll', onScroll, { passive: true });
    el.addEventListener('wheel', onWheel, { passive: true });
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: true });
    el.addEventListener('pointerdown', onPointerDown, { passive: true });
    el.addEventListener('keydown', onKeyDown);

    // Observe both DOM mutations (new messages, streaming chunks) and
    // size changes (images decoding, code blocks reflowing, composer
    // resizing). We mirror the library's own observation surface so
    // we react to the same events at the same time — just with a
    // looser at-bottom check, and rAF-coalesced so we read layout at
    // most once per paint.
    const resizeObserver = new ResizeObserver(scheduleContentCheck);
    resizeObserver.observe(el);

    const mutationObserver = new MutationObserver((mutations) => {
      // Skip pure style attribute changes — the library's
      // ThreadViewportSlack writes inline styles in response to
      // viewport changes, and reacting to those would create a
      // feedback loop. Same filter the library uses internally.
      const hasRelevantMutation = mutations.some(
        (m) => m.type !== 'attributes' || m.attributeName !== 'style',
      );
      if (hasRelevantMutation) scheduleContentCheck();
    });
    mutationObserver.observe(el, {
      childList: true,
      subtree: true,
      attributes: true,
      characterData: true,
    });

    return () => {
      el.removeEventListener('scroll', onScroll);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('pointerdown', onPointerDown);
      el.removeEventListener('keydown', onKeyDown);
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      if (rafHandle !== null) cancelAnimationFrame(rafHandle);
    };
    // viewportRef is stable across renders; threshold and
    // ignoreGesturesWithin are primitives, so re-running on change is
    // cheap and correct.
  }, [viewportRef, threshold, ignoreGesturesWithin]);

  return { showScrollToBottom, scrollToBottom };
}
