import { useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchBeadsStatus, type BeadsStatus } from './beadsApi';

export type { BeadsStatus, BeadsTicket } from './beadsApi';

export function useBeadsStatus(
  directory: string | undefined,
  remoteId: string | undefined,
  visible: boolean,
) {
  const queryClient = useQueryClient();
  const queryKey = ['beads-status', directory, remoteId] as const;
  return useQuery<BeadsStatus>({
    queryKey,
    enabled: !!directory && !!remoteId,
    refetchInterval: (query) => visible && query.state.data?.available ? 30_000 : false,
    refetchIntervalInBackground: false,
    queryFn: async ({ signal }) => {
      const next = await fetchBeadsStatus(directory!, remoteId!, signal);
      const previous = queryClient.getQueryData<BeadsStatus>(queryKey);
      if (previous?.available && next.available && next.error) {
        return {
          ...next,
          tickets: previous.tickets ?? next.tickets,
        };
      }
      return next;
    },
  });
}
