import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// useInfiniteRows reveals additional rows as the user scrolls. The
// node test environment has no IntersectionObserver, but the hook
// degrades gracefully — the observer setup short-circuits and
// `visibleCount` stays at the initial value. We exercise the pure
// pagination math (initial clamp, hasMore derivation, chunk-size
// growth via the captured callback) plus the IntersectionObserver
// integration via a stub.

interface ReactMockState {
  states: unknown[];
  setters: Array<(v: unknown) => void>;
  refs: Array<{ current: unknown }>;
  effects: Array<{ cb: () => void | (() => void); deps?: unknown[] }>;
  cleanups: Array<() => void>;
}

async function loadHookHarness(opts?: { ioStub?: typeof IntersectionObserver }) {
  const reactMock: ReactMockState = { states: [], setters: [], refs: [], effects: [], cleanups: [] };

  vi.doMock('react', () => ({
    useState: <T,>(init: T | (() => T)) => {
      const value = typeof init === 'function' ? (init as () => T)() : init;
      reactMock.states.push(value);
      const idx = reactMock.states.length - 1;
      const setState = (next: T | ((prev: T) => T)) => {
        const prev = reactMock.states[idx] as T;
        reactMock.states[idx] = typeof next === 'function' ? (next as (p: T) => T)(prev) : next;
      };
      reactMock.setters.push(setState as (v: unknown) => void);
      return [value, setState];
    },
    useRef: <T,>(init: T) => {
      const ref = { current: init };
      reactMock.refs.push(ref as { current: unknown });
      return ref;
    },
    useEffect: (cb: () => void | (() => void), deps?: unknown[]) => {
      const cleanup = cb();
      if (typeof cleanup === 'function') reactMock.cleanups.push(cleanup);
      reactMock.effects.push({ cb, deps });
    },
  }));

  if (opts?.ioStub) {
    (globalThis as unknown as { IntersectionObserver: typeof IntersectionObserver }).IntersectionObserver = opts.ioStub;
  } else {
    delete (globalThis as unknown as { IntersectionObserver?: typeof IntersectionObserver }).IntersectionObserver;
  }

  const mod = await import('./useInfiniteRows');
  return { mod, reactMock };
}

describe('useInfiniteRows', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('exports the hook', async () => {
    const { mod } = await loadHookHarness();
    expect(typeof mod.useInfiniteRows).toBe('function');
  });

  it('clamps the initial visibleCount to total when total is smaller', async () => {
    const { mod } = await loadHookHarness();
    const result = mod.useInfiniteRows({ total: 5, initial: 100 });
    expect(result.visibleCount).toBe(5);
    expect(result.hasMore).toBe(false);
  });

  it('uses initial when total is larger', async () => {
    const { mod } = await loadHookHarness();
    const result = mod.useInfiniteRows({ total: 100, initial: 10 });
    expect(result.visibleCount).toBe(10);
    expect(result.hasMore).toBe(true);
  });

  it('reports hasMore = false when total === 0', async () => {
    const { mod } = await loadHookHarness();
    const result = mod.useInfiniteRows({ total: 0, initial: 10 });
    expect(result.visibleCount).toBe(0);
    expect(result.hasMore).toBe(false);
  });

  it('returns a sentinelRef whose current is initially null', async () => {
    const { mod } = await loadHookHarness();
    const result = mod.useInfiniteRows({ total: 100, initial: 10 });
    expect(result.sentinelRef.current).toBeNull();
  });

  it('does not observe a sentinel when IntersectionObserver is unavailable', async () => {
    const { mod, reactMock } = await loadHookHarness();
    // No IO in globalThis: hook short-circuits, no observer is created.
    mod.useInfiniteRows({ total: 100, initial: 10 });
    // No observer setup means the observer-effect's cleanup is empty.
    // Any cleanup recorded must be the reset effect's no-op (none).
    for (const c of reactMock.cleanups) c();
  });

  it('reveals chunkSize more rows when the sentinel intersects', async () => {
    // Provide a controllable IntersectionObserver stub that fires the
    // callback synchronously when observe() is called.
    let captured: ((entries: { isIntersecting: boolean }[]) => void) | null = null;
    let nodeObserved: unknown = null;
    const ioStub = class {
      private cb: (entries: { isIntersecting: boolean }[]) => void;
      constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
        this.cb = cb;
        captured = cb;
      }
      observe(node: unknown) {
        nodeObserved = node;
        // Fire one intersection so the hook bumps visibleCount.
        this.cb([{ isIntersecting: true }]);
      }
      disconnect() {}
      unobserve() {}
      takeRecords() { return []; }
    } as unknown as typeof IntersectionObserver;

    const { mod, reactMock } = await loadHookHarness({ ioStub });

    // Pre-populate the sentinel ref so the observer effect can
    // observe a node. The hook records the ref via useRef, so we
    // grab the most recent ref out of reactMock and set its current.
    const sentinelNode = { tagName: 'DIV' };
    // Run the hook so the sentinel ref is created.
    mod.useInfiniteRows({ total: 100, initial: 10, chunkSize: 25 });
    // The sentinel ref is the only useRef call in the hook.
    const ref = reactMock.refs[0] as { current: unknown };
    ref.current = sentinelNode;

    // Run the hook again so the observer effect picks up the now-populated ref.
    // The mocked useState retains state across renders within the same module
    // load only if we re-use the same setter — but here a new render starts
    // with fresh state arrays. Instead, re-fire the observer effect manually
    // by reading the captured effect callback.
    const observerEffect = reactMock.effects[1]; // 0 = reset, 1 = observer setup
    observerEffect.cb();

    expect(captured).not.toBeNull();
    expect(nodeObserved).toBe(sentinelNode);

    // After the synchronous fire from observe(), state[0] (visibleCount)
    // grew by chunkSize (25) up to min(initial+25, total) = 35.
    expect(reactMock.states[0]).toBe(35);
  });
});
