import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionChanges } from './api';

// useSessionChanges follows the same shape as useSessionInfo —
// debounced refetch on dirtyTick, abort on session change / unmount,
// EMPTY_CHANGES fallback. Tests mirror the useSessionInfo harness:
// mock React + apiStore + DebouncedTrigger, drive the hook directly,
// and assert behaviour through the captured setters and call counts.

interface ReactMockState {
  states: unknown[];
  setters: Array<(v: unknown) => void>;
  refs: Array<{ current: unknown }>;
  effects: Array<{ cb: () => void | (() => void); deps?: unknown[] }>;
  cleanups: Array<() => void>;
}

interface MockTrigger {
  bump: ReturnType<typeof vi.fn> & (() => void);
  reset: ReturnType<typeof vi.fn> & (() => void);
  flushNow: ReturnType<typeof vi.fn> & (() => void);
  cancel: ReturnType<typeof vi.fn> & (() => void);
  fire: () => void;
}

async function loadHookHarness(opts?: {
  getSessionChangesImpl?: (id: string, signal: AbortSignal) => Promise<SessionChanges>;
}) {
  const reactMock: ReactMockState = { states: [], setters: [], refs: [], effects: [], cleanups: [] };
  const trigger: MockTrigger = (() => {
    let cb: () => void = () => {};
    return {
      bump: vi.fn(),
      reset: vi.fn(),
      flushNow: vi.fn(),
      cancel: vi.fn(),
      fire: () => cb(),
      _setCallback(next: () => void) { cb = next; },
    } as unknown as MockTrigger & { _setCallback: (cb: () => void) => void };
  })();

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
    useCallback: <F extends (...a: unknown[]) => unknown>(fn: F) => fn,
  }));

  vi.doMock('./debouncedTrigger', () => ({
    DebouncedTrigger: class {
      constructor(cb: () => void) {
        (trigger as unknown as { _setCallback: (cb: () => void) => void })._setCallback(cb);
      }
      bump() { (trigger.bump as () => void)(); }
      reset() { (trigger.reset as () => void)(); }
      flushNow() { (trigger.flushNow as () => void)(); }
      cancel() { (trigger.cancel as () => void)(); }
    },
  }));

  const getSessionChanges = opts?.getSessionChangesImpl
    ? vi.fn(opts.getSessionChangesImpl)
    : vi.fn().mockResolvedValue({
        sessionId: 'sess-1',
        supported: true,
        totalAdditions: 1,
        totalDeletions: 0,
        filesChanged: 1,
        files: [],
      } satisfies SessionChanges);

  vi.doMock('./apiStore', () => ({
    useApiStore: (selector: (state: { getSessionChanges: typeof getSessionChanges }) => unknown) =>
      selector({ getSessionChanges }),
  }));

  const mod = await import('./useSessionChanges');
  return { mod, reactMock, getSessionChanges, trigger };
}

describe('useSessionChanges', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('exports the hook', async () => {
    const { mod } = await loadHookHarness();
    expect(typeof mod.useSessionChanges).toBe('function');
  });

  it('returns EMPTY_CHANGES when disabled', async () => {
    const { mod, reactMock, getSessionChanges } = await loadHookHarness();
    const result = mod.useSessionChanges('sess-1', { enabled: false });
    expect(result.loading).toBe(false);
    expect(reactMock.states[0]).toMatchObject({ supported: false, sessionId: '' });
    expect(getSessionChanges).not.toHaveBeenCalled();
  });

  it('fires the initial fetch with an AbortSignal', async () => {
    const { mod, getSessionChanges } = await loadHookHarness();
    const result = mod.useSessionChanges('sess-1');
    expect(getSessionChanges).toHaveBeenCalledTimes(1);
    expect(getSessionChanges).toHaveBeenCalledWith('sess-1', expect.any(AbortSignal));
    expect(result.loading).toBe(true);
  });

  it('aborts in-flight requests on cleanup', async () => {
    const { mod, reactMock } = await loadHookHarness();
    mod.useSessionChanges('sess-1');
    expect(reactMock.cleanups.length).toBeGreaterThan(0);
    for (const c of reactMock.cleanups) c();
  });

  it('refresh() flushes the trigger when enabled', async () => {
    const { mod, trigger } = await loadHookHarness();
    const result = mod.useSessionChanges('sess-1');
    result.refresh();
    expect(trigger.flushNow).toHaveBeenCalledTimes(1);
  });

  it('refresh() is a no-op when disabled', async () => {
    const { mod, trigger } = await loadHookHarness();
    const result = mod.useSessionChanges('sess-1', { enabled: false });
    result.refresh();
    expect(trigger.flushNow).not.toHaveBeenCalled();
  });

  it('skips the fetch when no sessionId is provided', async () => {
    const { mod, getSessionChanges } = await loadHookHarness();
    mod.useSessionChanges(undefined);
    expect(getSessionChanges).not.toHaveBeenCalled();
  });
});
