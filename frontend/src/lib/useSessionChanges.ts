import { useCallback, useEffect, useRef, useState } from 'react';
import { useApiStore } from './apiStore';
import type { SessionChanges } from './api';
import { DebouncedTrigger } from './debouncedTrigger';

// Inner debounce window. A live session can fire many edit/write part
// updates in quick succession; coalescing them into one fetch every
// 500 ms keeps the request rate sane while still feeling responsive.
const REFETCH_DEBOUNCE_MS = 500;
// Hard ceiling on how long a busy stream of dirty ticks can keep the
// fetch at bay. After this much time has elapsed since the *first*
// tick of the current burst, a fetch fires regardless. Without this
// bound, a session that emits an edit every 400 ms would never let
// the inner timer settle.
const REFETCH_MAX_WAIT_MS = 5000;

// Empty payload used when the hook is disabled or the platform
// doesn't support file-change aggregation. Stable identity so
// re-renders don't churn referential equality.
const EMPTY_CHANGES: SessionChanges = {
  sessionId: '',
  supported: false,
  totalAdditions: 0,
  totalDeletions: 0,
  filesChanged: 0,
  files: [],
};

export interface UseSessionChangesResult {
  data: SessionChanges | null;
  loading: boolean;
  error: string | null;
  /**
   * Manually trigger an immediate refetch, bypassing the debounce.
   * Wired to the user-facing "Refresh" button. Safe to call when
   * `enabled` is false (no-op).
   */
  refresh: () => void;
}

export interface UseSessionChangesOptions {
  /**
   * When false, the hook returns synchronously with EMPTY_CHANGES and
   * never fires a request. Use this to skip the call when the
   * platform has fileChanges=false (no point asking).
   */
  enabled?: boolean;
  /**
   * Tick that re-triggers a debounced fetch when its value increments.
   * The session-detail page increments this whenever an edit/write
   * part arrives via SSE. The exact value isn't meaningful — only the
   * change.
   */
  dirtyTick?: number;
}

/**
 * useSessionChanges fetches /api/session/{id}/changes and re-fetches
 * whenever `options.dirtyTick` changes. Refetch timing follows
 * DebouncedTrigger: a 500 ms inner debounce that resets on every
 * tick, with a 5 s hard ceiling so a continuously-busy session still
 * sees periodic updates.
 *
 * Returns `{ data, loading, error, refresh }`. When `enabled` is
 * false, returns a synchronous EMPTY_CHANGES with `loading: false`
 * so the caller can render a "Not supported" empty state without a
 * flash of skeleton.
 */
export function useSessionChanges(
  sessionId: string | undefined,
  { enabled = true, dirtyTick = 0 }: UseSessionChangesOptions = {},
): UseSessionChangesResult {
  const [data, setData] = useState<SessionChanges | null>(null);
  const [loading, setLoading] = useState(enabled && !!sessionId);
  const [error, setError] = useState<string | null>(null);

  const getSessionChanges = useApiStore((s) => s.getSessionChanges);

  // Tracks the last successfully-fetched session, so a session change
  // resets state immediately rather than waiting for the next fetch
  // to complete.
  const lastSessionRef = useRef<string | undefined>(undefined);
  // AbortController for the in-flight request; cancelled when the
  // session changes, the component unmounts, or a new fetch starts.
  const abortRef = useRef<AbortController | null>(null);
  // The actual fetch worker. Captured in a ref so the trigger
  // callback always sees the latest sessionId / dependencies without
  // re-creating the trigger object.
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
      setData(enabled ? null : EMPTY_CHANGES);
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
      // Cancel any in-flight request so we don't race ourselves.
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      getSessionChanges(sessionId, controller.signal)
        .then((resp) => {
          if (controller.signal.aborted) return;
          setData(resp);
          setLoading(false);
          setError(null);
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          // AbortError is expected on session change / unmount; the
          // signal-aborted branch above already covers most cases,
          // but a fetch can reject with name === 'AbortError' too.
          if (err instanceof Error && err.name === 'AbortError') return;
          const message = err instanceof Error ? err.message : 'Failed to load changes';
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
    // We intentionally don't include `data` here — including it would
    // re-trigger the effect on every successful fetch and cause a
    // refetch loop. dirtyTick is the explicit "you should refetch"
    // signal; sessionId / enabled are the other reasons to (re-)init.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled, dirtyTick, getSessionChanges]);

  // Cancel pending timers on unmount; recreate-on-mount via the lazy
  // init above means a remount works without leaking a dead trigger.
  useEffect(() => {
    return () => {
      triggerRef.current?.cancel();
    };
  }, []);

  // refresh bypasses the debounce. Stable identity via useCallback so
  // consumers can pass it to memoised buttons without re-renders.
  const refresh = useCallback(() => {
    if (!enabled || !sessionId) return;
    triggerRef.current?.flushNow();
  }, [enabled, sessionId]);

  return { data, loading, error, refresh };
}
