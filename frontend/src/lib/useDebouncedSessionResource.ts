import { useCallback, useEffect, useRef, useState } from 'react';
import { DebouncedTrigger } from './debouncedTrigger';

// Inner debounce window. A live session can fire many edit/write part
// updates in quick succession; coalescing them into one fetch every
// 500 ms keeps the request rate sane while still feeling responsive.
const REFETCH_DEBOUNCE_MS = 500;
// Hard ceiling on how long a busy stream of dirty ticks can keep the
// fetch at bay. After this much time has elapsed since the *first*
// tick of the current burst, a fetch fires regardless.
const REFETCH_MAX_WAIT_MS = 5000;

export interface DebouncedSessionResourceOptions {
  /**
   * When false, the hook returns synchronously with the `emptyValue` and
   * never fires a request. Use this to skip the call when the platform
   * doesn't support the resource in question.
   */
  enabled?: boolean;
  /**
   * Tick that re-triggers a debounced fetch when its value increments.
   * Typically wired to the session-detail SSE dirty counter.
   */
  dirtyTick?: number;
}

export interface DebouncedSessionResourceResult<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  /**
   * Manually trigger an immediate refetch, bypassing the debounce.
   * Safe to call when `enabled` is false (no-op).
   */
  refresh: () => void;
}

/**
 * useDebouncedSessionResource is a generic base for hooks that fetch a
 * per-session resource and re-fetch whenever a `dirtyTick` counter
 * increments. Refetch timing follows DebouncedTrigger: a 500 ms inner
 * debounce that resets on every tick, with a 5 s hard ceiling so a
 * continuously-busy session still sees periodic updates.
 *
 * Callers supply:
 *   - `fetch`: async function `(sessionId, signal) => T`
 *   - `emptyValue`: stable object returned when `enabled` is false
 *   - `fallbackError`: error message prefix used when `fetch` throws a
 *     non-Error value (e.g. `'Failed to load changes'`)
 *
 * Both `useSessionChanges` and `useSessionInfo` are thin wrappers around
 * this hook; their data shapes and empty values differ but the
 * debounce/abort/session-change logic is identical.
 */
export function useDebouncedSessionResource<T>(
  sessionId: string | undefined,
  fetch: (id: string, signal: AbortSignal) => Promise<T>,
  emptyValue: T,
  fallbackError: string,
  { enabled = true, dirtyTick = 0 }: DebouncedSessionResourceOptions = {},
): DebouncedSessionResourceResult<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(enabled && !!sessionId);
  const [error, setError] = useState<string | null>(null);

  // Tracks the last successfully-fetched session so a session change
  // resets state immediately rather than waiting for the next fetch.
  const lastSessionRef = useRef<string | undefined>(undefined);
  // AbortController for the in-flight request; cancelled on session
  // change, unmount, or when a new fetch starts.
  const abortRef = useRef<AbortController | null>(null);
  // Actual fetch worker. Captured in a ref so the trigger callback
  // always sees the latest sessionId / fetch without re-creating the
  // trigger object.
  const fetchRef = useRef<() => void>(() => {});
  // Lazily created on first effect run; persists across renders.
  const triggerRef = useRef<DebouncedTrigger | null>(null);
  if (triggerRef.current === null) {
    triggerRef.current = new DebouncedTrigger(
      () => fetchRef.current(),
      { debounceMs: REFETCH_DEBOUNCE_MS, maxWaitMs: REFETCH_MAX_WAIT_MS },
    );
  }

  useEffect(() => {
    if (!enabled || !sessionId) {
      setData(enabled ? null : emptyValue);
      setLoading(false);
      setError(null);
      triggerRef.current?.reset();
      return;
    }

    // Reset state on session change so the previous session's data
    // doesn't briefly flash for the new one.
    if (lastSessionRef.current !== sessionId) {
      setData(null);
      setLoading(true);
      setError(null);
      lastSessionRef.current = sessionId;
      triggerRef.current?.reset();
    }

    const fetchNow = () => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      fetch(sessionId, controller.signal)
        .then((resp) => {
          if (controller.signal.aborted) return;
          setData(resp);
          setLoading(false);
          setError(null);
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof Error && err.name === 'AbortError') return;
          const message = err instanceof Error ? err.message : fallbackError;
          setError(message);
          setLoading(false);
        });
    };
    fetchRef.current = fetchNow;

    // First load on mount / session change: fire immediately, no
    // debounce. dirtyTick changes during the same session use the
    // DebouncedTrigger.
    if (data === null) {
      fetchNow();
    } else {
      triggerRef.current?.bump();
    }

    return () => {
      abortRef.current?.abort();
    };
    // We intentionally exclude `data` to avoid a refetch-on-every-success
    // loop. dirtyTick / sessionId / enabled are the explicit re-trigger
    // signals; fetch is stable across renders (from apiStore selector).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled, dirtyTick, fetch]);

  // Cancel pending timers on unmount so dead triggers don't fire after
  // the component is gone.
  useEffect(() => {
    return () => {
      triggerRef.current?.cancel();
    };
  }, []);

  // refresh bypasses the debounce. Stable identity via useCallback so
  // consumers can pass it to memoised buttons without extra re-renders.
  const refresh = useCallback(() => {
    if (!enabled || !sessionId) return;
    triggerRef.current?.flushNow();
  }, [enabled, sessionId]);

  return { data, loading, error, refresh };
}
