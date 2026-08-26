// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useGitInfo } from './useGitInfo';

afterEach(() => vi.unstubAllGlobals());

it('clears branch data when the owner changes', async () => {
  const fetcher = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ '/repo': { branch: 'old' } }), { status: 200 }))
    .mockReturnValueOnce(new Promise(() => {}));
  vi.stubGlobal('fetch', fetcher);
  const { result, rerender } = renderHook(
    ({ remoteId }) => useGitInfo(['/repo'], remoteId),
    { initialProps: { remoteId: 'old-owner' } },
  );
  await waitFor(() => expect(result.current.infos['/repo']?.branch).toBe('old'));

  rerender({ remoteId: 'new-owner' });
  await waitFor(() => expect(result.current.infos).toEqual({}));
});
