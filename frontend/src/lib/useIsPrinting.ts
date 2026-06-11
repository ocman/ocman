import { useEffect, useState } from 'react';

/**
 * Returns true while the page is being printed (or rendered to PDF).
 *
 * Most of ocman's collapsed tool blocks collapse via CSS `max-height`,
 * so the print stylesheet alone can reveal them. A few blocks, though,
 * gate their body on React state (`{expanded && ...}`) and so are
 * absent from the DOM when collapsed — CSS can't bring them back.
 * Components can OR this flag into their `expanded` state so the
 * printed transcript is complete:
 *
 * ```ts
 * const isPrinting = useIsPrinting();
 * const effectiveExpanded = expanded || isPrinting;
 * ```
 *
 * Backed by `matchMedia('print')` plus the `beforeprint`/`afterprint`
 * events for browsers (Safari) that don't toggle the print media query
 * reliably. SSR / non-browser environments return false.
 */
export function useIsPrinting(): boolean {
  // Lazy initializer reads the print media query once on mount instead
  // of calling setState synchronously inside the effect (which triggers
  // cascading renders). The effect below only subscribes for changes.
  const [printing, setPrinting] = useState(
    () => typeof window !== 'undefined' && !!window.matchMedia?.('print').matches,
  );

  useEffect(() => {
    if (typeof window === 'undefined') return;

    const onBefore = () => setPrinting(true);
    const onAfter = () => setPrinting(false);
    window.addEventListener('beforeprint', onBefore);
    window.addEventListener('afterprint', onAfter);

    // matchMedia('print') is true during the print snapshot in
    // Chromium-based browsers and when DevTools emulates print media.
    const mql = window.matchMedia?.('print');
    const onChange = (e: MediaQueryListEvent) => setPrinting(e.matches);
    mql?.addEventListener('change', onChange);

    return () => {
      window.removeEventListener('beforeprint', onBefore);
      window.removeEventListener('afterprint', onAfter);
      mql?.removeEventListener('change', onChange);
    };
  }, []);

  return printing;
}
