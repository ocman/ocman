import { useEffect, useState, type RefObject } from 'react';

/**
 * True once `ref` comes within `margin` px of the visible area of its
 * closest `rootSelector` ancestor (the scroll container; the viewport when
 * none matches); stays true. Without IntersectionObserver it is true
 * immediately. The root is found by selector, not computed style, because
 * reading styles per mounted element forces a style recalc each time.
 */
export function useNearViewport(ref: RefObject<Element | null>, rootSelector: string, margin = 1000): boolean {
  const [near, setNear] = useState(() => typeof IntersectionObserver === 'undefined');
  useEffect(() => {
    const el = ref.current;
    if (near || !el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) setNear(true);
    }, { root: el.closest(rootSelector), rootMargin: `${margin}px 0px` });
    io.observe(el);
    return () => io.disconnect();
  }, [ref, near, rootSelector, margin]);
  return near;
}
