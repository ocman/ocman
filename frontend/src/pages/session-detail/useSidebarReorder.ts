import { useLayoutEffect, useRef } from 'react';
import type { RefObject } from 'react';

/** FLIP only when row order changes, so token updates don't restart animations. */
export function useSidebarReorder(containerRef: RefObject<HTMLDivElement | null>, view: string) {
  const previous = useRef({ view, keys: '', tops: new Map<string, number>() });
  const animations = useRef<Animation[]>([]);

  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const rows = [...container.querySelectorAll<HTMLElement>('[data-session-key]')];
    const keys = JSON.stringify(rows.map((row) => row.dataset.sessionKey));
    if (previous.current.view === view && previous.current.keys === keys) return;
    for (const animation of animations.current) animation.cancel();
    animations.current = [];
    const origin = container.getBoundingClientRect().top - container.scrollTop;
    const tops = new Map(rows.map((row) => [row.dataset.sessionKey!, row.getBoundingClientRect().top - origin]));
    const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? true;
    if (!reduced && previous.current.view === view) {
      for (const row of rows) {
        const key = row.dataset.sessionKey!;
        const before = previous.current.tops.get(key);
        const delta = before === undefined ? 0 : before - tops.get(key)!;
        if (delta && row.animate) {
          animations.current.push(row.animate([
            { transform: `translateY(${delta}px)` }, { transform: 'translateY(0)' },
          ], { duration: 180, easing: 'ease-out' }));
        }
      }
    }
    previous.current = { view, keys, tops };
  });

  useLayoutEffect(() => () => {
    for (const animation of animations.current) animation.cancel();
  }, []);
}
