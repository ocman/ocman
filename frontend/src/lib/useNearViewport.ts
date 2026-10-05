import { useEffect, useState, type RefObject } from 'react';

function scrollParent(el: Element): Element | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p;
  }
  return null;
}

/**
 * True once `ref` comes within `margin` px of its scroll container's visible
 * area; stays true. Without IntersectionObserver it is true immediately.
 */
export function useNearViewport(ref: RefObject<Element | null>, margin = 1000): boolean {
  const [near, setNear] = useState(() => typeof IntersectionObserver === 'undefined');
  useEffect(() => {
    const el = ref.current;
    if (near || !el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) setNear(true);
    }, { root: scrollParent(el), rootMargin: `${margin}px 0px` });
    io.observe(el);
    return () => io.disconnect();
  }, [ref, near, margin]);
  return near;
}
