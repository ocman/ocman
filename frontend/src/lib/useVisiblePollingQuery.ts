import { useQuery, type QueryKey } from '@tanstack/react-query';
import { withDeadline } from './coalescedRefresh';
import { useDocumentVisible } from './usePanelVisible';

interface VisiblePollingQuery<T> {
  queryKey: QueryKey;
  /** Receives a signal that aborts on unmount or after `timeoutMs`. */
  run: (signal: AbortSignal) => Promise<T>;
  intervalMs: number;
  timeoutMs: number;
  enabled?: boolean;
}

/** Polls while the document is visible; each request is bounded by a deadline. */
export function useVisiblePollingQuery<T>({ queryKey, run, intervalMs, timeoutMs, enabled = true }: VisiblePollingQuery<T>) {
  const visible = useDocumentVisible();
  return useQuery({
    queryKey,
    queryFn: ({ signal }) => withDeadline(timeoutMs, run, signal),
    enabled: enabled && visible,
    refetchInterval: intervalMs,
  });
}
