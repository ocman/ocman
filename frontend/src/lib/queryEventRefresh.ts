import type { QueryClient, QueryKey, QueryFilters } from '@tanstack/react-query';
import { eventRefresh } from './eventRefresh';

export function queryEventRefresh(client: QueryClient, queryKey: QueryKey, predicate?: QueryFilters['predicate']) {
  const filters = { queryKey, predicate };
  return eventRefresh(async () => {
    // Finish older reads first: simply ignoring invalidation during a fetch
    // could lose the new/renamed/archived row carried by this event.
    await Promise.all(client.getQueryCache().findAll(filters).map(query => query.promise?.catch(() => {})));
    await client.invalidateQueries(filters, { cancelRefetch: false });
  });
}
