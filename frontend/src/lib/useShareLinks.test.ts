// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useShareLinks } from './useShareLinks';

describe('useShareLinks', () => {
  it('falls back to a generic message when load rejects with a non-Error', async () => {
    const load = () => Promise.reject('nope');
    const { result } = renderHook(() => useShareLinks(load, async () => {}));
    await waitFor(() => expect(result.current.error).toBe('Failed to load share links'));
    expect(result.current.loaded).toBe(false);
  });

  it('ignores a load that resolves after unmount', async () => {
    let resolve!: (v: { token: string; url: string }[]) => void;
    const load = () => new Promise<{ token: string; url: string }[]>((r) => { resolve = r; });
    const { result, unmount } = renderHook(() => useShareLinks(load, async () => {}));
    unmount();
    resolve([{ token: 't', url: 'u' }]);
    await Promise.resolve();
    expect(result.current.links).toEqual([]);
  });

  it('revokes through the owner callback and drops the link', async () => {
    const link = { token: 't', url: 'u' };
    const load = vi.fn(async () => [link]);
    const revokeLink = vi.fn(async () => {});
    const { result } = renderHook(() => useShareLinks(load, revokeLink));
    await waitFor(() => expect(result.current.links).toEqual([link]));
    await act(() => result.current.revoke(link));
    expect(revokeLink).toHaveBeenCalledWith(link);
    expect(result.current.links).toEqual([]);
  });
});
