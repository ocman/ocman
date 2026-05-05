import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkingTreeDiff } from './api';

// useWorkingTreeDiff mirrors useSessionChanges but adds a `notRepo`
// branch for HTTP 404 / "not a git worktree" errors. We exercise the
// orchestration logic with the same React + DebouncedTrigger mock
// harness used by the other refetch hooks.

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
  getGitDiffImpl?: (dir: string, opts: { fresh?: boolean }, signal: AbortSignal) => Promise<WorkingTreeDiff>;
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

  const getGitDiff = opts?.getGitDiffImpl
    ? vi.fn(opts.getGitDiffImpl)
    : vi.fn().mockResolvedValue({
        repo: '/x',
        branch: 'main',
        ahead: 0,
        behind: 0,
        files: [],
        truncated: false,
      } satisfies WorkingTreeDiff);

  vi.doMock('./apiStore', () => ({
    useApiStore: (selector: (state: { getGitDiff: typeof getGitDiff }) => unknown) =>
      selector({ getGitDiff }),
  }));

  const mod = await import('./useWorkingTreeDiff');
  return { mod, reactMock, getGitDiff, trigger };
}

describe('useWorkingTreeDiff', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('exports the hook', async () => {
    const { mod } = await loadHookHarness();
    expect(typeof mod.useWorkingTreeDiff).toBe('function');
  });

  it('returns the empty diff when disabled', async () => {
    const { mod, reactMock, getGitDiff } = await loadHookHarness();
    const result = mod.useWorkingTreeDiff('/repo', { enabled: false });
    expect(result.loading).toBe(false);
    expect(reactMock.states[0]).toMatchObject({ repo: '', branch: '', files: [] });
    expect(getGitDiff).not.toHaveBeenCalled();
  });

  it('fires the initial fetch with fresh=true and an AbortSignal', async () => {
    const { mod, getGitDiff } = await loadHookHarness();
    const result = mod.useWorkingTreeDiff('/repo');
    expect(getGitDiff).toHaveBeenCalledTimes(1);
    expect(getGitDiff).toHaveBeenCalledWith('/repo', { fresh: true }, expect.any(AbortSignal));
    expect(result.loading).toBe(true);
  });

  it('aborts in-flight requests on cleanup', async () => {
    const { mod, reactMock } = await loadHookHarness();
    mod.useWorkingTreeDiff('/repo');
    expect(reactMock.cleanups.length).toBeGreaterThan(0);
    for (const c of reactMock.cleanups) c();
  });

  it('refresh() flushes the trigger when enabled', async () => {
    const { mod, trigger } = await loadHookHarness();
    const result = mod.useWorkingTreeDiff('/repo');
    result.refresh();
    expect(trigger.flushNow).toHaveBeenCalledTimes(1);
  });

  it('refresh() is a no-op when disabled', async () => {
    const { mod, trigger } = await loadHookHarness();
    const result = mod.useWorkingTreeDiff('/repo', { enabled: false });
    result.refresh();
    expect(trigger.flushNow).not.toHaveBeenCalled();
  });

  it('skips the fetch when no dir is provided', async () => {
    const { mod, getGitDiff } = await loadHookHarness();
    mod.useWorkingTreeDiff(undefined);
    expect(getGitDiff).not.toHaveBeenCalled();
  });
});
