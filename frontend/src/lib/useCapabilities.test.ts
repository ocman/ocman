import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CapabilitiesResponse } from './api';

// useCapabilities owns a module-level cache + an in-flight Promise +
// a subscriber set, so tests reset modules between cases to start
// from a fresh state. The hook itself can't be rendered without
// @testing-library/react. We exercise the underlying logic by
// driving the React mock to capture the state setter, kicking the
// load via the public hook, draining the resulting Promise chain,
// and reading back the final state via the captured setter.

function makeResponse(overrides: Partial<CapabilitiesResponse> = {}): CapabilitiesResponse {
  return {
    platforms: [
      {
        id: 'opencode',
        displayName: 'OpenCode',
        available: true,
        capabilities: {
          composer: true,
          respondPermission: true,
          respondQuestion: true,
          abort: true,
          compact: true,
          events: true,
          agentCatalog: true,
          modelCatalog: true,
          slashCommands: true,
          shellExec: true,
          fileChanges: true,
          sessionInfo: true,
          liveConnectionHint: 'opencode --port 0',
          autoApprove: true,
        },
      },
    ],
    worktreeSessions: false,
    ...overrides,
  };
}

/**
 * Set up a fresh useCapabilities module with mocked React + api,
 * captured state setters, and a deterministic flush helper. Every
 * test starts here so module-level caches don't leak between cases.
 */
async function loadFreshModule(resp: CapabilitiesResponse) {
  const capabilities = vi.fn().mockResolvedValue(resp);
  vi.doMock('./api', () => ({ api: { capabilities } }));

  // Track all registered effects and the latest state per render so
  // we can simulate React's re-render after a setState call.
  const setters: Array<(v: unknown) => void> = [];
  const states: unknown[] = [];

  vi.doMock('react', () => ({
    useState: <T,>(init: T | (() => T)) => {
      const value = typeof init === 'function' ? (init as () => T)() : init;
      states.push(value);
      const idx = states.length - 1;
      const setState = (next: T | ((prev: T) => T)) => {
        const prev = states[idx] as T;
        const resolved = typeof next === 'function' ? (next as (p: T) => T)(prev) : next;
        states[idx] = resolved;
      };
      setters.push(setState as (v: unknown) => void);
      return [value, setState];
    },
    useEffect: (cb: () => unknown) => { cb(); },
  }));

  const mod = await import('./useCapabilities');

  /** Drain microtasks so the loadCapabilities promise resolves. */
  const flush = async () => {
    for (let i = 0; i < 5; i++) await Promise.resolve();
  };

  /**
   * Drive the public hook once to register the subscriber. After
   * `flush()` the subscriber has fired with the network response, so
   * `latestState()` returns what React would see post-re-render.
   */
  const tickHook = (hook: () => unknown) => {
    states.length = 0; // reset per-call to mimic a fresh render
    setters.length = 0;
    return hook();
  };

  return { mod, capabilities, flush, tickHook, states, setters };
}

describe('useCapabilities module surface', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('exports the documented hook surface', async () => {
    const { mod } = await loadFreshModule(makeResponse());
    expect(typeof mod.useCapabilities).toBe('function');
    expect(typeof mod.usePlatformCapabilities).toBe('function');
    expect(typeof mod.useMultiPlatform).toBe('function');
    expect(typeof mod.useWorktreeSessions).toBe('function');
  });
});

describe('useCapabilities caching', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('issues exactly one network call across many initial subscribers', async () => {
    const { mod, capabilities, flush, tickHook } = await loadFreshModule(makeResponse());

    tickHook(() => mod.useCapabilities());
    tickHook(() => mod.useCapabilities());
    tickHook(() => mod.useCapabilities());

    await flush();
    expect(capabilities).toHaveBeenCalledTimes(1);
  });

  it('subsequent renders after the cache is warm do not call api again', async () => {
    const { mod, capabilities, flush, tickHook } = await loadFreshModule(makeResponse());

    tickHook(() => mod.useCapabilities());
    await flush();
    expect(capabilities).toHaveBeenCalledTimes(1);

    tickHook(() => mod.useCapabilities());
    tickHook(() => mod.useCapabilities());
    expect(capabilities).toHaveBeenCalledTimes(1);
  });

  it('returns the cached response from the lazy initialiser after warm-up', async () => {
    const resp = makeResponse();
    const { mod, flush, tickHook, states } = await loadFreshModule(resp);

    // First render: states[0] is null (cache empty), set after flush.
    tickHook(() => mod.useCapabilities());
    await flush();

    // Second render observes the warmed-up cache via the lazy
    // initialiser, so states[0] is already the response.
    const second = tickHook(() => mod.useCapabilities());
    expect(second).toEqual(resp);
    expect(states[0]).toEqual(resp);
  });
});

describe('usePlatformCapabilities lookup', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('falls back to all-false flags when no platformID is given', async () => {
    const { mod, flush, tickHook } = await loadFreshModule(makeResponse());
    // Warm cache once.
    tickHook(() => mod.useCapabilities());
    await flush();

    const caps = tickHook(() => mod.usePlatformCapabilities(undefined)) as { composer: boolean };
    expect(caps.composer).toBe(false);
  });

  it('falls back to all-false when the platform is not registered', async () => {
    const { mod, flush, tickHook } = await loadFreshModule(makeResponse());
    tickHook(() => mod.useCapabilities());
    await flush();

    const caps = tickHook(() => mod.usePlatformCapabilities('nonexistent')) as {
      composer: boolean;
      respondPermission: boolean;
      liveConnectionHint?: string;
    };
    expect(caps.composer).toBe(false);
    expect(caps.respondPermission).toBe(false);
    expect(caps.liveConnectionHint).toBe('');
  });

  it('returns the registered platform capabilities when matched', async () => {
    const { mod, flush, tickHook } = await loadFreshModule(makeResponse());
    tickHook(() => mod.useCapabilities());
    await flush();

    const caps = tickHook(() => mod.usePlatformCapabilities('opencode')) as {
      composer: boolean;
      events: boolean;
      liveConnectionHint?: string;
    };
    expect(caps.composer).toBe(true);
    expect(caps.events).toBe(true);
    expect(caps.liveConnectionHint).toBe('opencode --port 0');
  });
});

describe('useMultiPlatform / useWorktreeSessions', () => {
  beforeEach(() => { vi.resetModules(); });
  afterEach(() => { vi.restoreAllMocks(); });

  it('useMultiPlatform is false when only one platform is registered', async () => {
    const { mod, flush, tickHook } = await loadFreshModule(makeResponse());
    tickHook(() => mod.useCapabilities());
    await flush();
    expect(tickHook(() => mod.useMultiPlatform())).toBe(false);
  });

  it('useMultiPlatform is true with two or more platforms', async () => {
    const resp = makeResponse({
      platforms: [
        ...makeResponse().platforms,
        {
          id: 'claude-code',
          displayName: 'Claude Code',
          available: true,
          capabilities: { ...makeResponse().platforms[0].capabilities },
        },
      ],
    });
    const { mod, flush, tickHook } = await loadFreshModule(resp);
    tickHook(() => mod.useCapabilities());
    await flush();
    expect(tickHook(() => mod.useMultiPlatform())).toBe(true);
  });

  it('useMultiPlatform is false when remotes share one base platform', async () => {
    // Multi-remote registers a compound "r-<id>:opencode" per remote;
    // they're all OpenCode, so the badge must stay hidden.
    const resp = makeResponse({
      platforms: [
        ...makeResponse().platforms,
        {
          id: 'r-abc123:opencode',
          displayName: 'OpenCode',
          available: true,
          capabilities: { ...makeResponse().platforms[0].capabilities },
        },
      ],
    });
    const { mod, flush, tickHook } = await loadFreshModule(resp);
    tickHook(() => mod.useCapabilities());
    await flush();
    expect(tickHook(() => mod.useMultiPlatform())).toBe(false);
  });

  it('useWorktreeSessions reflects the response flag exactly', async () => {
    const off = await loadFreshModule(makeResponse({ worktreeSessions: false }));
    off.tickHook(() => off.mod.useCapabilities());
    await off.flush();
    expect(off.tickHook(() => off.mod.useWorktreeSessions())).toBe(false);

    vi.resetModules();

    const on = await loadFreshModule(makeResponse({ worktreeSessions: true }));
    on.tickHook(() => on.mod.useCapabilities());
    await on.flush();
    expect(on.tickHook(() => on.mod.useWorktreeSessions())).toBe(true);
  });
});
