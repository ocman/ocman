// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { useShareLinks } from './useShareLinks';

vi.mock('./api', () => ({ api: { revokeShareLink: vi.fn() } }));

describe('useShareLinks', () => {
  it('falls back to a generic message when load rejects with a non-Error', async () => {
    const load = () => Promise.reject('nope');
    const { result } = renderHook(() => useShareLinks(load, () => 'ses'));
    await waitFor(() => expect(result.current.error).toBe('Failed to load share links'));
    expect(result.current.loaded).toBe(false);
  });

  it('ignores a load that resolves after unmount', async () => {
    let resolve!: (v: { token: string; url: string }[]) => void;
    const load = () => new Promise<{ token: string; url: string }[]>((r) => { resolve = r; });
    const { result, unmount } = renderHook(() => useShareLinks(load, () => 'ses'));
    unmount();
    resolve([{ token: 't', url: 'u' }]);
    await Promise.resolve();
    expect(result.current.links).toEqual([]);
  });
});
