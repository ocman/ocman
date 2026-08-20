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
});
