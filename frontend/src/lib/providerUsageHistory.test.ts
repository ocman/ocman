import { describe, expect, it, vi } from 'vitest';
import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from './api.requests';
import { useProviderUsageHistory } from './providerUsageHistory';

vi.mock('@tanstack/react-query', () => ({ useQuery: vi.fn() }));
vi.mock('./api.requests', () => ({ fetchJSON: vi.fn() }));

describe('useProviderUsageHistory', () => {
  it('scopes requests by period and refreshes on the sampling cadence', async () => {
    useProviderUsageHistory(30);
    const options = vi.mocked(useQuery).mock.lastCall?.[0];
    expect(options).toMatchObject({ queryKey: ['providerUsageHistory', 30], staleTime: 60_000, refetchInterval: 300_000 });
    const signal = new AbortController().signal;
    const queryFn = options?.queryFn as (context: { signal: AbortSignal }) => Promise<unknown>;
    const result = { sampleIntervalMs: 300_000, samples: [] };
    vi.mocked(fetchJSON).mockResolvedValue(result);
    expect(await queryFn({ signal })).toBe(result);
    expect(fetchJSON).toHaveBeenCalledWith('/api/provider-usage/history?days=30', signal);
  });
});
