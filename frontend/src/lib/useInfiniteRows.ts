import { useEffect, useRef, useState } from 'react';

// Default chunk size for incremental row reveal. Callers can override.
const DEFAULT_CHUNK_SIZE = 500;

// Margin (in pixels) below the viewport that still triggers a load.
// Negative top + bottom values would be invalid, so this is set as
// a `rootMargin` extension below the visible area: as soon as the
// sentinel comes within 200 px of the viewport bottom, we reveal
// the next chunk. Smaller margins feel snappier but produce less
// runway for the next batch to render before the user reaches it.
const ROOT_MARGIN = '0px 0px 200px 0px';

export interface UseInfiniteRowsOptions {
  /**
   * The total number of rows we could potentially render.
   */
  total: number;
  /**
   * How many rows to mount on first render. After that, each
   * intersection of the sentinel reveals `chunkSize` more.
   */
  initial: number;
  /**
   * How many rows to add per chunk. Defaults to DEFAULT_CHUNK_SIZE.
   */
  chunkSize?: number;
}

export interface UseInfiniteRowsResult<E extends HTMLElement = HTMLDivElement> {
  /** How many rows the caller should currently render. */
  visibleCount: number;
  /**
   * Ref the caller attaches to a sentinel element placed AFTER the
   * last visible row. When this ref enters the scroll viewport the
   * hook bumps `visibleCount` by `chunkSize`. Generic over the
   * concrete element type so callers can pass it through to e.g.
   * `<div ref={...}>` without a cast.
   */
  sentinelRef: React.RefObject<E | null>;
  /**
   * True when there are still rows beyond `visibleCount`. Useful for
   * conditionally rendering the sentinel.
   */
  hasMore: boolean;
}

/**
 * useInfiniteRows reveals more rows of a list as the user scrolls
 * toward the bottom. Used by DiffView and RawDiffView so large
 * diffs don't mount thousands of DOM nodes up-front, but also don't
 * require the user to click a "Show more" button.
 *
 * The caller is responsible for:
 *  1. Slicing its row array to `visibleCount` before mapping to JSX.
 *  2. Rendering a small sentinel element (a 1-px div is fine) at the
 *     end of the list and attaching `sentinelRef` to it.
 *
 * The hook uses IntersectionObserver, which is supported in every
 * browser ocman targets. SSR / non-DOM environments degrade
 * gracefully (the observer setup is a no-op and visibleCount stays
 * at `initial`).
 */
export function useInfiniteRows<E extends HTMLElement = HTMLDivElement>({
  total,
  initial,
  chunkSize = DEFAULT_CHUNK_SIZE,
}: UseInfiniteRowsOptions): UseInfiniteRowsResult<E> {
  // Initial value clamps to total so a small list never advertises
  // more rows than it can produce.
  const [visibleCount, setVisibleCount] = useState(() => Math.min(initial, total));
  const sentinelRef = useRef<E | null>(null);

  // Reset the count whenever `total` changes (e.g. the user opened a
  // different file). Without this, switching from a 5000-row file to
  // a 50-row one would leave visibleCount stuck at 5000 — harmless,
  // but it makes the hook's contract less obvious.
  useEffect(() => {
    setVisibleCount(Math.min(initial, total));
  }, [total, initial]);

  useEffect(() => {
    if (visibleCount >= total) return;
    const node = sentinelRef.current;
    if (!node || typeof IntersectionObserver === 'undefined') return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setVisibleCount((c) => Math.min(c + chunkSize, total));
            // We don't disconnect here — the same sentinel will be
            // re-observed for the next chunk. The hook loops until
            // visibleCount >= total, at which point the effect's
            // early return prevents further observation.
          }
        }
      },
      { rootMargin: ROOT_MARGIN },
    );

    observer.observe(node);
    return () => observer.disconnect();
  }, [visibleCount, total, chunkSize]);

  return {
    visibleCount,
    sentinelRef,
    hasMore: visibleCount < total,
  };
}
