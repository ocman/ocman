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
import { renderHook } from '@testing-library/react';
import { useRef, type RefObject } from 'react';
import { useStickyBottom } from './useStickyBottom';

interface FakeViewport {
  el: HTMLElement;
  setScrollTop: (value: number) => void;
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
    useStickyBottom(ref as RefObject<HTMLElement | null>);
    return null;
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

    // Advance one frame; the coalesced handler runs once.
    vi.advanceTimersByTime(20);
    expect(fv.scrollTopReads - baselineScrollTopReads).toBe(1);
    expect(fv.scrollHeightReads).toBeGreaterThan(0);
  });

  it('schedules a fresh frame when mutations arrive after a previous flush', () => {
    const fv = makeFakeViewport({ scrollTop: 950, scrollHeight: 1100, clientHeight: 1000 });
    setupHook(fv);

    const baseline = fv.scrollTopReads;

    // Frame 1: 10 mutations → 1 layout read
    for (let i = 0; i < 10; i++) resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollTopReads - baseline).toBe(1);

    // Frame 2: 10 more mutations → 1 more layout read
    for (let i = 0; i < 10; i++) resizeCbs[0]?.();
    vi.advanceTimersByTime(20);
    expect(fv.scrollTopReads - baseline).toBe(2);
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

    fv.setScrollTop(100);
    fv.el.dispatchEvent(new Event('scroll'));
    resizeCbs[0]?.();
    vi.advanceTimersByTime(20);

    expect(fv.scrollToCalls).toBe(1);
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
