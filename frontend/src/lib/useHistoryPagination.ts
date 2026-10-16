import { useEffect, useRef, type RefObject } from 'react';

interface HistoryPaginationOptions {
  oldestMessageId?: string;
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
}

/** Keep paging while the beginning of the loaded history is visible. */
export function useHistoryPagination(
  viewportRef: RefObject<HTMLElement | null>,
  { oldestMessageId, hasMore, loadingMore, onLoadMore }: HistoryPaginationOptions,
) {
  const startRef = useRef<HTMLDivElement>(null);
  const attemptedRef = useRef<string | null>(null);

  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || !hasMore || loadingMore || !onLoadMore || typeof IntersectionObserver === 'undefined') return;
    // Some pages contain only hidden tool messages. The start marker still
    // lets those pages fetch their predecessor instead of leaving a blank view.
    const oldest = viewport.querySelector<HTMLElement>('[data-message-id]') ?? startRef.current;
    if (!oldest) return;
    const key = oldestMessageId ?? oldest.dataset.messageId ?? '';
    let active = true;
    const visible = new Set<Element>();
    const observer = new IntersectionObserver((entries) => {
      if (!active) return;
      for (const entry of entries) {
        if (entry.isIntersecting) visible.add(entry.target);
        else visible.delete(entry.target);
      }
      if (visible.size === 0) {
        attemptedRef.current = null;
      } else if (attemptedRef.current !== key) {
        // Do not loop on a failed request or a page that adds no history.
        // Progress changes the key; leaving and re-entering permits retry.
        attemptedRef.current = key;
        onLoadMore();
      }
    }, { root: viewport });
    observer.observe(oldest);
    if (startRef.current && startRef.current !== oldest) observer.observe(startRef.current);
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [viewportRef, oldestMessageId, hasMore, loadingMore, onLoadMore]);

  return startRef;
}
