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
});
