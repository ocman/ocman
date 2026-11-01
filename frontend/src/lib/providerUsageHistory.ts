import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from './api.requests';
import type { SubscriptionProviderUsage } from './api';

export interface SubscriptionUsageSample {
  sampledAt: number;
  sampleIntervalMs?: number;
  provider: SubscriptionProviderUsage;
}

export interface SubscriptionUsageHistory {
  sampleIntervalMs: number;
  samples: SubscriptionUsageSample[];
}

export function useProviderUsageHistory(days: number) {
  return useQuery({
    queryKey: ['providerUsageHistory', days],
    queryFn: ({ signal }) => fetchJSON<SubscriptionUsageHistory>(`/api/provider-usage/history?days=${days}`, signal),
    staleTime: 60_000,
    refetchInterval: 300_000,
  });
}
