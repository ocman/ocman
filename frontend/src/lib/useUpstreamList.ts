import { useRef, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { queryClient } from './queryClient';
import { useDocumentVisible } from './usePanelVisible';
import {
  fetchPRs, fetchIssues, UpstreamApiError,
  type PR, type Issue, type Pagination, type RateLimit, type StateFilter,
} from './upstreamApi';

export type UpstreamListItem = PR | Issue;

export interface UseUpstreamListResult<T extends UpstreamListItem> {
  items: T[];
  loading: boolean;
  error: UpstreamApiError | Error | null;
  pagination: Pagination;
  rateLimit: RateLimit;
  refresh: (fresh?: boolean) => void;
  setPage: (page: number) => void;
  page: number;
}

/** One owner-scoped page. Page changes keep rows; filter changes clear them. */
export function useUpstreamList<T extends UpstreamListItem>(opts: {
  kind: 'prs' | 'issues';
  dir: string;
  remoteId: string;
  remote: string;
  state: StateFilter;
  mine: string | undefined;
  enabled: boolean;
}): UseUpstreamListResult<T> {
  const visible = useDocumentVisible();
  const { kind, dir, remoteId, remote, state, mine, enabled } = opts;
  const filters = JSON.stringify([kind, dir, remoteId, remote, state, mine]);
  const [selection, setSelection] = useState({ filters, page: 1 });
  if (selection.filters !== filters) setSelection({ filters, page: 1 });
  const page = selection.filters === filters ? selection.page : 1;
  const fresh = useRef(false);
  const active = enabled && !!dir && !!remote;
  const query = useQuery({
    queryKey: ['upstream-list', filters, page],
    queryFn: async ({ signal }) => {
      const params = { dir, remoteId, remote, state, mine, page, signal, refresh: fresh.current };
      fresh.current = false;
      if (kind === 'prs') {
        const result = await fetchPRs(params);
        return { items: result.prs as T[], pagination: result.pagination, rateLimit: result.rateLimit };
      }
      const result = await fetchIssues(params);
      return { items: result.issues as T[], pagination: result.pagination, rateLimit: result.rateLimit };
    },
    enabled: active && visible,
    staleTime: 0,
    retry: false,
    placeholderData: (previous, previousQuery) => previousQuery?.queryKey[1] === filters ? keepPreviousData(previous) : undefined,
  }, queryClient);
  const error = query.error instanceof Error ? query.error : query.error ? new Error(String(query.error)) : null;
  return {
    items: active ? query.data?.items ?? [] : [],
    loading: active && (query.isFetching || query.isPending),
    error: active ? error : null,
    pagination: active ? query.data?.pagination ?? EMPTY_PAGINATION : EMPTY_PAGINATION,
    rateLimit: active && !query.isFetching ? query.data?.rateLimit ?? EMPTY_RATE_LIMIT : EMPTY_RATE_LIMIT,
    refresh: (refresh = true) => {
      fresh.current = refresh;
      if (active && visible) void query.refetch();
    },
    setPage: page => setSelection({ filters, page }),
    page,
  };
}

const EMPTY_PAGINATION: Pagination = { page: 1, hasMore: false };
const EMPTY_RATE_LIMIT: RateLimit = { limited: false };
