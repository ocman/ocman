// @vitest-environment jsdom
//
// reloadCapabilities re-fetches the agent catalog and the model list
// even though the session identity did not change — that's what makes
// a restarted OpenCode instance's config show up in the pickers.

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSessionCapabilities } from './useSessionCapabilities';
import { api } from '../../lib/api';

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
      id: 'remote-session', platform: 'r-box:opencode', liveConnection: true, directory: '/repo',
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

  it('falls back to historical models when the live catalog is unavailable', async () => {
    vi.mocked(api.sessionModels).mockRejectedValueOnce(new Error('offline'));
    storeState.getModels.mockResolvedValueOnce([{ provider: 'history', model: 'recent', count: 5 }]);
    const { result } = renderHook(() => useSessionCapabilities({
      id: 'offline-session', platform: 'opencode', liveConnection: false, directory: '/repo',
    }));
    act(() => result.current.refreshModels());
    await waitFor(() => expect(result.current.modelOptions).toEqual(['history/recent']));
    expect(result.current.modelEntries[0]).toEqual({ provider: 'history', model: 'recent' });
  });
});
