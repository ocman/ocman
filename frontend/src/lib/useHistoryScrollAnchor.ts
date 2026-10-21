import { useCallback, useLayoutEffect, useRef, type RefObject } from 'react';

interface Anchor {
  message: HTMLElement;
  offset: number;
}

/** Keep the message being read stationary throughout a history request. */
export function useHistoryScrollAnchor(
  viewportRef: RefObject<HTMLElement | null>,
  loadingMore: boolean | undefined,
  onLoadMore: (() => void) | undefined,
) {
  const anchorRef = useRef<Anchor | null>(null);

  const capture = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    const bounds = viewport.getBoundingClientRect();
    for (const message of viewport.querySelectorAll<HTMLElement>('[data-message-id]')) {
      const rect = message.getBoundingClientRect();
      if (rect.bottom > bounds.top && rect.top < bounds.bottom) {
        anchorRef.current = { message, offset: rect.top - bounds.top };
        return;
      }
    }
    anchorRef.current = null;
  }, [viewportRef]);

  const loadMore = useCallback(() => {
    capture();
    onLoadMore?.();
  }, [capture, onLoadMore]);

  const restore = useCallback(() => {
    const viewport = viewportRef.current;
    const anchor = anchorRef.current;
    if (viewport && anchor && viewport.contains(anchor.message)) {
      viewport.scrollTop += anchor.message.getBoundingClientRect().top
        - viewport.getBoundingClientRect().top - anchor.offset;
    }
  }, [viewportRef]);

  // Run on each commit, including insertion/removal of the loading indicator.
  // Measure the remaining displacement, not total growth: browsers may have
  // already applied native scroll anchoring, and replies can grow below us.
  useLayoutEffect(() => {
    restore();
  });

  useLayoutEffect(() => {
    if (loadingMore || !anchorRef.current) return;
    // The external thread runtime commits its messages after our parent. Keep
    // the snapshot through that commit and its viewport layout effects.
    const frame = requestAnimationFrame(() => {
      restore();
      anchorRef.current = null;
    });
    return () => cancelAnimationFrame(frame);
  });

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    const observer = new MutationObserver(restore);
    observer.observe(viewport, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [viewportRef, restore]);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || !loadingMore) return;
    // Honour movement while waiting for the network instead of restoring the
    // position from the beginning of the request.
    viewport.addEventListener('scroll', capture);
    return () => viewport.removeEventListener('scroll', capture);
  }, [viewportRef, loadingMore, capture]);

  return onLoadMore ? loadMore : undefined;
}
