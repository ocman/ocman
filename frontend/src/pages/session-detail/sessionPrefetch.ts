import { useCallback, useEffect, useRef } from 'react';
import { api } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';

/** Hover dwell before a sidebar row warms its session. Filters a pointer sweep. */
export const PREFETCH_DELAY_MS = 150;
/** Same first page useSession loads, so the warmed entry is what it renders. */
const PREFETCH_PAGE_SIZE = 30;

const inFlight = new Set<string>();

/**
 * Warm the session cache for `id` so opening it renders at once. useSession
 * shows the cached copy behind a "checking for updates" note and refreshes
 * it, so a stale warm entry is harmless. Skips sessions already cached or
 * being fetched; failures are dropped (the click path loads normally).
 */
export async function prefetchSession(id: string, platform?: string): Promise<void> {
  if (useApiStore.getState().getCachedSession(id) || inFlight.has(id)) return;
  inFlight.add(id);
  try {
    // Same routing rule as useSession: only remote owners need the hint.
    const routed = platform?.startsWith('r-') ? platform : undefined;
    const detail = await api.session(id, PREFETCH_PAGE_SIZE, 0, undefined, routed);
    // An open session may have cached a newer view meanwhile; keep it.
    if (useApiStore.getState().getCachedSession(id)) return;
    useApiStore.getState().setCachedSession(id, {
      ...detail,
      session: { ...detail.session, contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount },
      totalMessages: detail.totalMessages || detail.session.messageCount || 0,
    });
  } catch {
    // ponytail: best-effort warm-up; the real open reports its own errors.
  } finally {
    inFlight.delete(id);
  }
}

/** Pointer/focus handlers that prefetch a session after a short dwell. */
export function useHoverPrefetch(id: string, platform?: string) {
  const timer = useRef<number | null>(null);
  const cancel = useCallback(() => {
    if (timer.current !== null) window.clearTimeout(timer.current);
    timer.current = null;
  }, []);
  const start = useCallback(() => {
    cancel();
    timer.current = window.setTimeout(() => {
      timer.current = null;
      void prefetchSession(id, platform);
    }, PREFETCH_DELAY_MS);
  }, [cancel, id, platform]);
  useEffect(() => cancel, [cancel]);
  return { onPointerEnter: start, onPointerLeave: cancel, onFocus: start, onBlur: cancel };
}
