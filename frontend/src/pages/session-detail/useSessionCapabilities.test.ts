// @vitest-environment jsdom
//
// reloadCapabilities re-fetches the agent catalog and the model list
// even though the session identity did not change — that's what makes
// a restarted OpenCode instance's config show up in the pickers.

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSessionCapabilities } from './useSessionCapabilities';
import { api } from '../../lib/api';
import { clearModelCatalogCache, readModelCatalog, writeModelCatalog } from '../../lib/modelCatalogCache';

// The state object must be stable across renders: an unstable
// getModels would re-create refreshModels and re-run the fetch effect
// forever.
const storeState = { getModels: vi.fn().mockResolvedValue([]) };

vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) => selector(storeState),
}));

vi.mock('../../lib/api', () => ({
  api: {
    agents: vi.fn().mockResolvedValue([{ name: 'build' }]),
    sessionModels: vi.fn().mockResolvedValue({ models: [{ provider: 'p', model: 'm' }] }),
    addFavorite: vi.fn().mockResolvedValue(undefined),
    removeFavorite: vi.fn().mockResolvedValue(undefined),
  },
}));

describe('useSessionCapabilities.reloadCapabilities', () => {
  it('re-fetches agents and models', async () => {
    const { result } = renderHook(() => useSessionCapabilities({
      id: 'sess-1',
      platform: 'opencode',
      liveConnection: true,
      directory: '/p',
      sessionLoaded: true,
    }));

    await waitFor(() => expect(result.current.agentsLoaded).toBe(true));
    expect(vi.mocked(api.agents)).toHaveBeenCalledTimes(1);
    const modelCalls = vi.mocked(api.sessionModels).mock.calls.length;

    await act(async () => { result.current.reloadCapabilities(); });

    await waitFor(() => expect(vi.mocked(api.agents)).toHaveBeenCalledTimes(2));
    expect(vi.mocked(api.sessionModels).mock.calls.length).toBeGreaterThan(modelCalls);
  });

  it('reconciles favorite changes with the owning platform and reverts failed changes', async () => {
    const { result } = renderHook(() => useSessionCapabilities({
      id: 'remote-session', platform: 'r-box:opencode', liveConnection: true, directory: '/repo', sessionLoaded: true,
    }));
    await waitFor(() => expect(result.current.modelEntries).toHaveLength(1));
    await act(() => result.current.handleToggleFavorite('p', 'm', true));
    expect(api.addFavorite).toHaveBeenCalledWith('r-box:opencode', 'p', 'm');
    await act(() => result.current.handleToggleFavorite('p', 'm', false));
    expect(api.removeFavorite).toHaveBeenCalledWith('r-box:opencode', 'p', 'm');
    vi.mocked(api.addFavorite).mockRejectedValueOnce(new Error('offline'));
    await act(() => result.current.handleToggleFavorite('p', 'm', true));
    expect(result.current.modelEntries[0].isFavorite).toBe(false);
  });

  it('fetches models once a session loads and again only when it goes live', async () => {
    vi.mocked(api.sessionModels).mockClear();
    const { rerender } = renderHook(
      (props: { id: string; live: boolean; loaded: boolean }) => useSessionCapabilities({
        id: props.id, platform: 'opencode', liveConnection: props.live, directory: '/repo', sessionLoaded: props.loaded,
      }),
      { initialProps: { id: 'a', live: false, loaded: false } },
    );
    const calls = () => vi.mocked(api.sessionModels).mock.calls.map((c) => c[0]);
    expect(calls()).toEqual([]);
    rerender({ id: 'a', live: false, loaded: true });
    rerender({ id: 'a', live: false, loaded: true });
    expect(calls()).toEqual(['a']);
    rerender({ id: 'a', live: true, loaded: true });
    rerender({ id: 'a', live: false, loaded: true });
    expect(calls()).toEqual(['a', 'a']);
    rerender({ id: 'b', live: true, loaded: true });
    expect(calls()).toEqual(['a', 'a', 'b']);
  });

  it('drops the previous session catalog when the route changes before the next loads', async () => {
    let resolveA!: (value: { models: { provider: string; model: string }[] }) => void;
    vi.mocked(api.sessionModels).mockClear();
    vi.mocked(api.sessionModels).mockReturnValueOnce(new Promise((resolve) => { resolveA = resolve; }) as never);
    const { result, rerender } = renderHook(
      (props: { id: string; loaded: boolean }) => useSessionCapabilities({
        id: props.id, platform: 'opencode', liveConnection: true, directory: '/repo', sessionLoaded: props.loaded,
      }),
      { initialProps: { id: 'a', loaded: true } },
    );
    rerender({ id: 'b', loaded: false });
    await act(async () => { resolveA({ models: [{ provider: 'from', model: 'a' }] }); });
    expect(result.current.modelOptions).toEqual([]);
  });

  it('refetches when a session goes live after loading offline behind a live one', () => {
    vi.mocked(api.sessionModels).mockClear();
    const { rerender } = renderHook(
      (props: { id: string; live: boolean }) => useSessionCapabilities({
        id: props.id, platform: 'opencode', liveConnection: props.live, directory: '/repo', sessionLoaded: true,
      }),
      { initialProps: { id: 'a', live: true } },
    );
    // B arrives cached and offline while the port state still says A was live.
    rerender({ id: 'b', live: false });
    rerender({ id: 'b', live: true });
    expect(vi.mocked(api.sessionModels).mock.calls.map((c) => c[0])).toEqual(['a', 'b', 'b']);
  });

  it('falls back to historical models when the live catalog is unavailable', async () => {
    vi.mocked(api.sessionModels).mockRejectedValueOnce(new Error('offline'));
    storeState.getModels.mockResolvedValueOnce([{ provider: 'history', model: 'recent', count: 5 }]);
    const { result } = renderHook(() => useSessionCapabilities({
      id: 'offline-session', platform: 'opencode', liveConnection: false, directory: '/repo', sessionLoaded: false,
    }));
    act(() => result.current.refreshModels());
    await waitFor(() => expect(result.current.modelOptions).toEqual(['history/recent']));
    expect(result.current.modelEntries[0]).toEqual({ provider: 'history', model: 'recent', isAvailable: true });
  });
});

describe('useSessionCapabilities model catalog cache', () => {
  const live = { hasProviders: true, models: [{ provider: 'anthropic', model: 'opus', isAvailable: true, isFavorite: true }] };
  const render = (id: string, directory = '/proj') => renderHook(() => useSessionCapabilities({
    id, platform: 'opencode', liveConnection: true, directory, sessionLoaded: true,
  }));

  it('does not flag models unavailable when the provider catalog is missing', async () => {
    clearModelCatalogCache();
    vi.mocked(api.sessionModels).mockResolvedValueOnce({ hasProviders: false, models: [{ provider: 'anthropic', model: 'opus' }] });
    const { result } = render('s1');
    await waitFor(() => expect(result.current.modelEntries).toHaveLength(1));
    expect(result.current.modelEntries[0].isAvailable).toBe(true);
  });

  it('keeps the last live catalog when a refresh has no provider data', async () => {
    clearModelCatalogCache();
    vi.mocked(api.sessionModels).mockResolvedValueOnce(live);
    const first = render('s1');
    await waitFor(() => expect(first.result.current.modelEntries).toEqual(live.models));
    first.unmount();

    // The next session in the project opens with the cached list, and a
    // response without providers does not replace it.
    let resolve!: (v: unknown) => void;
    vi.mocked(api.sessionModels).mockReturnValueOnce(new Promise((r) => { resolve = r; }) as never);
    const { result } = render('s2');
    expect(result.current.modelEntries).toEqual(live.models);
    await act(async () => { resolve({ hasProviders: false, models: [{ provider: 'anthropic', model: 'opus' }] }); });
    expect(result.current.modelEntries).toEqual(live.models);
  });

  it('seeds a new worktree from the newest catalog and survives a reload', () => {
    clearModelCatalogCache();
    writeModelCatalog('opencode', '/proj', live.models);
    expect(readModelCatalog('opencode', '/proj/.worktrees/x')).toEqual(live.models);
    expect(readModelCatalog('other', '/proj')).toBeUndefined();
    const stored = JSON.parse(localStorage.getItem('ocman.modelCatalog.v1')!);
    expect(stored['opencode\n/proj']).toEqual(live.models);
  });
});
