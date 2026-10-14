import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchJSON, postJSON } from './api.requests';

export interface BadgeSettings {
  sessions: boolean;
  primary: boolean;
  factory: boolean;
  routines: boolean;
}

const key = ['notification-badge-settings'];
const path = '/api/settings/notification-badge';

export function useBadgeSettings() {
  return useQuery({ queryKey: key, queryFn: ({ signal }) => fetchJSON<BadgeSettings>(path, signal), refetchInterval: 10_000 });
}

export function useSaveBadgeSettings() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (settings: Partial<BadgeSettings>) => postJSON<BadgeSettings>(path, settings),
    onMutate: () => client.cancelQueries({ queryKey: key }),
    onSuccess: (settings) => client.setQueryData(key, settings),
  });
}
