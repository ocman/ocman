// @vitest-environment jsdom
//
// Regression tests for the rAF-coalesced content-change pipeline in
// useStickyBottom.
//
// The hook observes the viewport with a MutationObserver and a
// ResizeObserver. During streaming (~30 deltas/sec), those fire
// synchronously on every DOM mutation. Without coalescing each
// invocation reads `scrollTop`/`scrollHeight`/`clientHeight`, which
// forces a synchronous layout — and dozens of forced layouts per
// second block the main thread, including click event dispatch.
// Users see this as "navigation only happens when the LLM finishes a
// complete block".
//
// The hook coalesces all observer fires into one rAF callback per
// frame: many mutations → one layout read → one scrollTo at most.
// These tests pin that contract by counting layout reads via spies on
// the viewport's scroll-related getters.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useRef, type RefObject } from 'react';
import { useStickyBottom } from './useStickyBottom';

interface FakeViewport {
  el: HTMLElement;
  setScrollTop: (value: number) => void;
  setScrollHeight: (value: number) => void;
  // Counters for the layout-forcing reads.
  scrollTopReads: number;
  scrollHeightReads: number;
  clientHeightReads: number;
  scrollToCalls: number;
}

function makeFakeViewport({
  scrollTop = 0,
  scrollHeight = 1000,
  clientHeight = 1000,
}: { scrollTop?: number; scrollHeight?: number; clientHeight?: number } = {}): FakeViewport {
  const el = document.createElement('div');
  document.body.appendChild(el);

  const fv: FakeViewport = {
    el,
    setScrollTop: (value) => { scrollTop = value; },
    setScrollHeight: (value) => { scrollHeight = value; },
    scrollTopReads: 0,
    scrollHeightReads: 0,
    clientHeightReads: 0,
    scrollToCalls: 0,
  };

  // Count getter reads — that's how we measure forced layouts.
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get() { fv.scrollTopReads += 1; return scrollTop; },
    set() { /* no-op for the test */ },
  });
  Object.defineProperty(el, 'scrollHeight', {
    configurable: true,
    get() { fv.scrollHeightReads += 1; return scrollHeight; },
  });
  Object.defineProperty(el, 'clientHeight', {
    configurable: true,
    get() { fv.clientHeightReads += 1; return clientHeight; },
  });
  el.scrollTo = vi.fn(() => { fv.scrollToCalls += 1; }) as unknown as typeof el.scrollTo;
  return fv;
}

// ResizeObserver and MutationObserver stubs that expose a `trigger`
// method so tests can simulate observed mutations without involving
// jsdom's actual observer machinery (which doesn't ship in jsdom for
// ResizeObserver and is sluggish for MutationObserver in batches).
type ResizeCb = () => void;
type MutationCb = (mutations: Array<{ type: string; attributeName?: string }>) => void;

let resizeCbs: ResizeCb[] = [];
let mutationCbs: MutationCb[] = [];

class StubResizeObserver {
  cb: ResizeCb;
  constructor(cb: ResizeCb) {
    this.cb = cb;
    resizeCbs.push(cb);
  }
  observe() {}
  disconnect() {
    resizeCbs = resizeCbs.filter((c) => c !== this.cb);
  }
}

class StubMutationObserver {
  cb: MutationCb;
  constructor(cb: MutationCb) {
    this.cb = cb;
    mutationCbs.push(cb);
  }
  observe() {}
  disconnect() {
    mutationCbs = mutationCbs.filter((c) => c !== this.cb);
  }
  takeRecords() { return []; }
}

beforeEach(() => {
  resizeCbs = [];
  mutationCbs = [];
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  vi.stubGlobal('MutationObserver', StubMutationObserver);
  // Drive rAF via fake timers so tests are deterministic.
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    return (setTimeout(() => cb(performance.now()), 16) as unknown as number);
  });
  vi.stubGlobal('cancelAnimationFrame', (handle: number) => {
    clearTimeout(handle as unknown as ReturnType<typeof setTimeout>);
  });
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

function setupHook(fv: FakeViewport) {
  return renderHook(() => {
    const ref = useRef<HTMLElement | null>(fv.el);
    return useStickyBottom(ref as RefObject<HTMLElement | null>);
  });
}

describe('useStickyBottom — rAF coalescing', () => {
  it('reads layout at most once per animation frame, no matter how many mutations fire', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    setupHook(fv);

    // Capture the baseline reads from initial setup (the hook
    // doesn't read on mount, but the observer constructors might).
    const baselineScrollTopReads = fv.scrollTopReads;

    // Fire the resize observer 50 times within a single frame.
    for (let i = 0; i < 50; i++) resizeCbs[0]?.();
    // Fire the mutation observer 50 times in the same frame too.
    for (let i = 0; i < 50; i++) {
      mutationCbs[0]?.([{ type: 'characterData' }]);
    }

    // No frame has elapsed yet — handler must NOT have read layout
    // because the rAF hasn't fired.
    expect(fv.scrollTopReads).toBe(baselineScrollTopReads);

    // Advance one frame; the coalesced handler runs once. A content tick
    // decides from the sticky mode alone, so it costs *no* scrollTop
    // read — 100 mutations force zero position reads, not one per frame.
    vi.advanceTimersByTime(20);
    expect(fv.scrollTopReads - baselineScrollTopReads).toBe(0);
    // The scroll itself still needs the target, so scrollHeight is read.
    expect(fv.scrollHeightReads).toBeGreaterThan(0);
    expect(fv.scrollToCalls).toBe(1);
  });

  it('schedules a fresh frame when mutations arrive after a previous flush', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    setupHook(fv);

    // Frame 1: 10 mutations → one coalesced flush
    for (let i = 0; i < 10; i++) resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(1);

    // Frame 2: 10 more mutations → one more flush, i.e. the rAF is
    // re-armed after a flush rather than latched shut.
    for (let i = 0; i < 10; i++) resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(2);
  });

  it('skips style-only mutations to avoid the library feedback loop', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    setupHook(fv);

    const baseline = fv.scrollTopReads;

    // Style-only mutations are filtered before scheduling rAF — no
    // frame should be scheduled at all.
    for (let i = 0; i < 50; i++) {
      mutationCbs[0]?.([{ type: 'attributes', attributeName: 'style' }]);
    }
    vi.advanceTimersByTime(50);
    expect(fv.scrollTopReads).toBe(baseline);
  });

  it('scrolls to the bottom when content grows while near-bottom', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    setupHook(fv);

    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
  });

  it('does not scroll when content grows while the user is reading older messages', () => {
    const fv = makeFakeViewport({ scrollTop: 100, scrollHeight: 5000, clientHeight: 1000 });
    setupHook(fv);

    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(0);
  });

  it('keeps the viewport in place when content changes after the user scrolls up', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(1);

    fv.el.dispatchEvent(new WheelEvent('wheel', { deltaY: -10 }));
    fv.setScrollTop(100);
    fv.el.dispatchEvent(new Event('scroll'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
  });

  // A wheel/touch gesture is applied on the compositor thread frames
  // before its `scroll` event is dispatched to JS. In that window the
  // geometry still reads "near the bottom", so a content tick landing
  // there used to conclude the user had not moved and scroll them back
  // down — the "it keeps yanking me to the bottom while I'm reading"
  // report. Acting on the gesture itself closes the window.
  it('disengages before an upward wheel scroll lands, so content does not chase the bottom', () => {
      const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
      setupHook(fv);

      // Engaged: a content tick follows the tail.
      resizeCbs[0]?.();
      vi.advanceTimersByTime(20);
      expect(fv.scrollToCalls).toBe(1);

      // The user scrolls up. The gesture fires; the `scroll` event has
      // not been dispatched yet, so the geometry is deliberately left
      // reading near-bottom to reproduce the stale read.
      fv.el.dispatchEvent(new WheelEvent('wheel', { deltaY: -10 }));

      resizeCbs[0]?.();
      vi.advanceTimersByTime(20);

      expect(fv.scrollToCalls).toBe(1);
  });

  it('re-engages once the offset lands back inside the near-bottom band', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    // Gesture away, then confirm the follow is off.
    fv.el.dispatchEvent(new WheelEvent('wheel', { deltaY: -10 }));
    fv.setScrollTop(100);
    fv.el.dispatchEvent(new Event('scroll'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(0);

    // Scroll back down into the band: follow resumes without the user
    // having to click the scroll-to-bottom button.
    fv.setScrollTop(1000);
    fv.el.dispatchEvent(new Event('scroll'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(1);
  });

  it('keeps following after a click inside the conversation', () => {
    const fv = makeFakeViewport({ scrollTop: 1000, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    fv.el.dispatchEvent(new Event('pointerdown'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
  });

  it('keeps following after a wheel-down at the bottom', () => {
    const fv = makeFakeViewport({ scrollTop: 1000, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    fv.el.dispatchEvent(new WheelEvent('wheel', { deltaY: 10 }));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
  });

  it('keeps following after a browser scroll event without an upward gesture', () => {
    const fv = makeFakeViewport({ scrollTop: 1000, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    fv.setScrollTop(800);
    fv.el.dispatchEvent(new Event('scroll'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
  });

  it('keeps following when content shrinks above the fold without a gesture', () => {
    // The message trim dropping older messages, or a tool block
    // collapsing, moves scrollTop without the user touching anything.
    // Geometry is no longer consulted on content ticks precisely so this
    // cannot be mistaken for intent in either direction.
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
    setupHook(fv);

    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(1);

    // Content above the viewport vanishes; the browser lowers scrollTop.
    // No gesture, no scroll event attributable to the user.
    fv.setScrollTop(400);
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(2);
  });

  describe('scroll-to-bottom affordance', () => {
    it('stays hidden while following the tail', () => {
      const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
      const { result } = setupHook(fv);

      resizeCbs[0]?.();
      vi.advanceTimersByTime(20);

      expect(result.current.showScrollToBottom).toBe(false);
    });

    it('appears once the user is well clear of the bottom', () => {
      const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
      const { result } = setupHook(fv);

      // Distance 700 — past the show threshold.
      fv.setScrollTop(300);
      act(() => { fv.el.dispatchEvent(new Event('scroll')); });

      expect(result.current.showScrollToBottom).toBe(true);
    });

    it('holds visibility steady across a streaming size jump inside the band', () => {
      // The flicker this replaces: a truncated bash block resolving moves
      // the distance by tens of pixels. Inside the band nothing changes.
      const fv = makeFakeViewport({ scrollTop: 850, scrollHeight: 2000, clientHeight: 1000 });
      const { result } = setupHook(fv);

      // Distance 150 — inside the 80..240 band, entered from hidden.
      act(() => { fv.el.dispatchEvent(new Event('scroll')); });
      expect(result.current.showScrollToBottom).toBe(false);

      fv.setScrollTop(800); // distance 200, still inside the band
      act(() => { fv.el.dispatchEvent(new Event('scroll')); });
      expect(result.current.showScrollToBottom).toBe(false);
    });

    it('appears when content grows below a disengaged viewport, with no scroll event', () => {
      // Nothing scrolls here, so only the content tick can notice. This
      // is why the tick still reads geometry on the disengaged path.
      //
      // Start disengaged but *inside* the band (distance 200), so the
      // affordance is hidden and only the growth can reveal it.
      const fv = makeFakeViewport({ scrollTop: 1000, scrollHeight: 2200, clientHeight: 1000 });
      const { result } = setupHook(fv);

      fv.el.dispatchEvent(new Event('wheel'));
      act(() => { fv.el.dispatchEvent(new Event('scroll')); });
      expect(result.current.showScrollToBottom).toBe(false);

      // A long reply streams in below the fold.
      fv.setScrollHeight(5000);

      act(() => {
        resizeCbs[0]?.();
        vi.advanceTimersByTime(20);
      });

      expect(result.current.showScrollToBottom).toBe(true);
    });

    it('re-engages follow and hides itself when invoked', () => {
      const fv = makeFakeViewport({ scrollTop: 100, scrollHeight: 5000, clientHeight: 1000 });
      const { result } = setupHook(fv);

      act(() => { fv.el.dispatchEvent(new Event('scroll')); });
      expect(result.current.showScrollToBottom).toBe(true);

      act(() => { result.current.scrollToBottom(); });
      expect(result.current.showScrollToBottom).toBe(false);
      expect(fv.scrollToCalls).toBe(1);

      // Follow is back on: the next content tick tracks the tail even
      // though the geometry still says we are far from it.
      act(() => {
        resizeCbs[0]?.();
        vi.advanceTimersByTime(20);
      });
      expect(fv.scrollToCalls).toBe(2);
    });
  });

  it('ignores gestures from an excluded subtree, so typing does not stop the follow', () => {
    // The composer renders inside the scroll container, so a pointerdown
    // on the textarea bubbles up to the viewport. Treating that as
    // "stop following" would break the ordinary act of clicking into the
    // composer to reply while the previous answer streams in.
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
    const composer = document.createElement('div');
    composer.className = 'oc-viewport-footer';
    const textarea = document.createElement('textarea');
    composer.appendChild(textarea);
    fv.el.appendChild(composer);

    renderHook(() => {
      const ref = useRef<HTMLElement | null>(fv.el);
      return useStickyBottom(ref as RefObject<HTMLElement | null>, {
        ignoreGesturesWithin: '.oc-viewport-footer',
      });
    });

    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(1);

    // Gesture from inside the composer — must not disengage.
    textarea.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(2);

    // A click on the transcript is not a scroll gesture either.
    fv.el.dispatchEvent(new Event('pointerdown'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollToCalls).toBe(3);
  });

  it('removes the gesture listeners on teardown', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 2000, clientHeight: 1000 });
    const { unmount } = setupHook(fv);
    const removeSpy = vi.spyOn(fv.el, 'removeEventListener');

    unmount();

    const removed = removeSpy.mock.calls.map(([type]) => type);
    expect(removed).toContain('wheel');
    expect(removed).toContain('touchmove');
    expect(removed).toContain('pointerdown');
    expect(removed).toContain('scroll');
  });

  it('cancels any pending rAF on teardown', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    const { unmount } = setupHook(fv);

    const baseline = fv.scrollTopReads;
    // Schedule a frame…
    resizeCbs[0]?.();
    // …then unmount before the frame fires.
    unmount();
    vi.advanceTimersByTime(20);
    // The coalesced handler must NOT have run after unmount.
    expect(fv.scrollTopReads).toBe(baseline);
  });
});
