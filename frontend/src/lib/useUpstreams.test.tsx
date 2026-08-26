// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from './upstreamApi';
import { useUpstreams } from './useUpstreams';

describe('useUpstreams', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('separates identical project paths by owner', async () => {
    const fetchUpstreams = vi.spyOn(api, 'fetchUpstreams').mockResolvedValue([]);
    const { rerender } = renderHook(
      ({ remoteId }) => useUpstreams('/repo', remoteId),
      { initialProps: { remoteId: 'machine-a' } },
    );

    await waitFor(() => expect(fetchUpstreams).toHaveBeenCalledTimes(1));
    rerender({ remoteId: 'machine-b' });

    await waitFor(() => expect(fetchUpstreams).toHaveBeenCalledTimes(2));
    expect(fetchUpstreams.mock.calls.map((call) => call[1])).toEqual(['machine-a', 'machine-b']);
  });

  it('does not expose the previous owner upstreams while loading', async () => {
    const snapshots: Array<{ owner: string; repos: string[] }> = [];
    vi.spyOn(api, 'fetchUpstreams')
      .mockResolvedValueOnce([{ remote: 'origin', host: 'github.com', type: 'github', repo: 'old/repo' }])
      .mockReturnValueOnce(new Promise(() => {}));
    const { result, rerender } = renderHook(
      ({ owner }) => {
        const value = useUpstreams('/repo', owner);
        snapshots.push({ owner, repos: value.upstreams.map((upstream) => upstream.repo) });
        return value;
      },
      { initialProps: { owner: 'old-owner' } },
    );
    await waitFor(() => expect(result.current.upstreams).toHaveLength(1));

    rerender({ owner: 'new-owner' });
    expect(result.current.upstreams).toEqual([]);
    expect(snapshots).not.toContainEqual({ owner: 'new-owner', repos: ['old/repo'] });
  });

  it('clears upstreams while a different project loads', async () => {
    const snapshots: Array<{ directory: string; repos: string[] }> = [];
    vi.spyOn(api, 'fetchUpstreams')
      .mockResolvedValueOnce([{ remote: 'origin', host: 'github.com', type: 'github', repo: 'old/repo' }])
      .mockReturnValueOnce(new Promise(() => {}));
    const { result, rerender } = renderHook(
      ({ directory }) => {
        const value = useUpstreams(directory, 'local');
        snapshots.push({ directory, repos: value.upstreams.map((upstream) => upstream.repo) });
        return value;
      },
      { initialProps: { directory: '/old' } },
    );
    await waitFor(() => expect(result.current.upstreams).toHaveLength(1));

    rerender({ directory: '/new' });
    expect(result.current.upstreams).toEqual([]);
    expect(result.current.loading).toBe(true);
    expect(snapshots).not.toContainEqual({ directory: '/new', repos: ['old/repo'] });
  });

  it('clears loading when disabled during a request', async () => {
    vi.spyOn(api, 'fetchUpstreams').mockReturnValue(new Promise(() => {}));
    const { result, rerender } = renderHook(
      ({ directory }) => useUpstreams(directory, 'local'),
      { initialProps: { directory: '/repo' as string | undefined } },
    );
    await waitFor(() => expect(result.current.loading).toBe(true));

    rerender({ directory: undefined });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeNull();
  });
});
