import type { QueryClient, QueryKey } from '@tanstack/react-query';
import { eventRefresh } from './eventRefresh';

export function queryEventRefresh(client: QueryClient, queryKey: QueryKey) {
  return eventRefresh(async () => {
    // Finish older reads first: simply ignoring invalidation during a fetch
    // could lose the new/renamed/archived row carried by this event.
    await Promise.all(client.getQueryCache().findAll({ queryKey }).map(query => query.promise?.catch(() => {})));
    await client.invalidateQueries({ queryKey }, { cancelRefetch: false });
  });
}
