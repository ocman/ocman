// @vitest-environment jsdom
import { waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionInfo } from './api';
import { renderHookWithQuery } from '../test/renderWithQuery';

const info: SessionInfo = {
  sessionId: 'sess-1',
  supported: true,
  context: { tokens: 1, cost: 0, estCost: 0 },
  tokens: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0 },
  mcpServers: [],
  lspServers: [],
  messages: { user: 1, assistant: 1 },
  commits: [],
};
const getSessionInfo = vi.fn<(id: string, signal?: AbortSignal, platform?: string) => Promise<SessionInfo>>(() => Promise.resolve(info));
vi.mock('./api', () => ({
  api: { sessionInfo: getSessionInfo },
}));

const { useSessionInfo } = await import('./useSessionInfo');

describe('useSessionInfo', () => {
  beforeEach(() => getSessionInfo.mockClear());

  it('returns EMPTY_INFO without fetching when disabled', () => {
    const { result } = renderHookWithQuery(() => useSessionInfo('sess-1', { enabled: false }));
    expect(result.current).toMatchObject({ loading: false, error: null, data: { supported: false, sessionId: '' } });
    expect(getSessionInfo).not.toHaveBeenCalled();
  });

  it('fetches the session info', async () => {
    const { result } = renderHookWithQuery(() => useSessionInfo('sess-1'));
    expect(result.current).toMatchObject({ data: null, loading: true });
    await waitFor(() => expect(result.current.data).toEqual(info));
    expect(getSessionInfo).toHaveBeenCalledWith('sess-1', expect.any(AbortSignal), undefined);
  });

  it('qualifies remote requests and cache entries by platform', async () => {
    const { client } = renderHookWithQuery(() => {
      useSessionInfo('same');
      useSessionInfo('same', { platformId: 'r-box:opencode' });
    });
    await waitFor(() => expect(getSessionInfo).toHaveBeenCalledTimes(2));
    expect(getSessionInfo).toHaveBeenCalledWith('same', expect.any(AbortSignal), 'r-box:opencode');
    expect(client.getQueryData(['session-info', 'r-box:opencode', 'same'])).toEqual(info);
  });

  it('skips the fetch when sessionId is missing', () => {
    renderHookWithQuery(() => useSessionInfo(undefined));
    expect(getSessionInfo).not.toHaveBeenCalled();
  });
});
