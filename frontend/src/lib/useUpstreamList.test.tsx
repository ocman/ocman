// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import * as api from './upstreamApi';
import { useUpstreamList } from './useUpstreamList';

beforeEach(() => vi.restoreAllMocks());

it('clears rows when the project changes', async () => {
  vi.spyOn(api, 'fetchPRs')
    .mockResolvedValueOnce({
      prs: [{ number: 7, title: 'old project' } as api.PR],
      pagination: { page: 1, hasMore: false },
      rateLimit: { limited: false },
    })
    .mockReturnValueOnce(new Promise(() => {}));

  const { result, rerender } = renderHook(
    ({ dir }) => useUpstreamList<api.PR>({
      kind: 'prs', dir, remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true,
    }),
    { initialProps: { dir: '/old' } },
  );
  await waitFor(() => expect(result.current.items).toHaveLength(1));

  rerender({ dir: '/new' });
  await waitFor(() => expect(result.current.items).toEqual([]));
});
