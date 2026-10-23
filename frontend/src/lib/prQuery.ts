import { useEffect, useRef, useState } from 'react';
import { hashKey, useQuery, type QueryKey } from '@tanstack/react-query';
import { queryClient } from './queryClient';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { PREVIEW_AUTH_EVENT } from './previews';
import { useDocumentVisible } from './usePanelVisible';

export interface PRPoll<T> {
  value?: T;
  nextPollAt?: number;
  error?: unknown;
  failures?: number;
  emptyResponses?: number;
  refresh?: boolean;
}

// Refresh also discards results retained by unmounted rows.
if (typeof window !== 'undefined') {
  window.addEventListener(PR_CHECKS_REFRESH_EVENT, (event) => {
    const repositories = (event as CustomEvent<string[]>).detail;
    queryClient.removeQueries({ predicate: ({ queryKey }) =>
      (queryKey[0] === 'pr-status' || queryKey[0] === 'pr-checks') &&
      (!repositories || repositories.includes(String(queryKey[1]))) });
  });
  window.addEventListener(PREVIEW_AUTH_EVENT, () => queryClient.removeQueries({ queryKey: ['pr-status'] }));
}

/** Query owns scheduling, shared requests and last-observer cancellation. */
export function usePRQuery<T>(key: QueryKey, visible: boolean, run: (signal: AbortSignal, previous: PRPoll<T> | undefined, refresh: boolean) => Promise<PRPoll<T>>, refreshOnMount: boolean) {
  const documentVisible = useDocumentVisible();
  const active = visible && documentVisible;
  const [generation, setGeneration] = useState(0);
  const fresh = useRef(refreshOnMount);
  const consumed = useRef(0);
  const initial = useRef(true);
  const [initialData] = useState(() => queryClient.getQueryData<PRPoll<T>>(key));
  useEffect(() => { initial.current = false; }, []);
  const repository = String(key[1]);
  const kind = key[0];
  useEffect(() => {
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      if (!repositories || repositories.includes(repository)) setGeneration((value) => value + 1);
    };
    const authChanged = () => { if (kind === 'pr-status') setGeneration((value) => value + 1); };
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    window.addEventListener(PREVIEW_AUTH_EVENT, authChanged);
    return () => {
      window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
      window.removeEventListener(PREVIEW_AUTH_EVENT, authChanged);
    };
  }, [repository, kind]);
  useEffect(() => { if (!active) fresh.current = refreshOnMount; }, [active, refreshOnMount]);
  const queryKey = key;
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => {
      const previous = refreshOnMount && initial.current ? undefined : queryClient.getQueryData<PRPoll<T>>(queryKey);
      const refresh = fresh.current || generation > consumed.current || (refreshOnMount && !previous);
      fresh.current = false;
      consumed.current = generation;
      return run(signal, previous, refresh);
    },
    enabled: active,
    subscribed: active,
    staleTime: ({ state }) => state.data?.nextPollAt ? Math.max(0, state.data.nextPollAt - state.dataUpdatedAt) : fresh.current ? 0 : Infinity,
    placeholderData: (previous, previousQuery) => hashKey(previousQuery?.queryKey ?? []) === hashKey(key) ? previous : undefined,
    retry: false,
    refetchOnMount: ({ state }) => refreshOnMount && (initial.current || !state.data?.nextPollAt) ? 'always' : true,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: ({ state }) => state.data?.nextPollAt ? Math.max(1, state.data.nextPollAt - Date.now()) : false,
  }, queryClient);
  useEffect(() => {
    if (query.data && !query.isPlaceholderData) { fresh.current = false; consumed.current = generation; }
  }, [query.data, query.isPlaceholderData, generation]);
  return { ...query, data: refreshOnMount && query.isFetching && query.data === initialData ? undefined : query.data };
}
