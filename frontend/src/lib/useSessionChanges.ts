import { useEffect, useRef, useState } from 'react';
import { useApiStore } from './apiStore';
import type { SessionChanges } from './api';

// Debounce window for re-fetching after the dirty flag flips. A live
// session can fire many edit/write part updates in quick succession;
// coalescing them into one fetch every 500ms keeps the request rate
// sane while still feeling responsive.
const REFETCH_DEBOUNCE_MS = 500;

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
 * whenever `options.dirtyTick` changes (debounced by REFETCH_DEBOUNCE_MS).
 *
 * Returns `{ data, loading, error }`. When `enabled` is false, returns
 * a synchronous EMPTY_CHANGES with `loading: false` so the caller can
 * render a "Not supported" empty state without a flash of skeleton.
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
  // Holds the pending debounce timer between dirty ticks.
  const timerRef = useRef<number | null>(null);
  // AbortController for the in-flight request; cancelled when the
  // session changes, the component unmounts, or a new fetch starts.
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!enabled || !sessionId) {
      setData(enabled ? null : EMPTY_CHANGES);
      setLoading(false);
      setError(null);
      return;
    }

    // Reset state on session change so the previous session's data
    // doesn't briefly flash for the new one.
    if (lastSessionRef.current !== sessionId) {
      setData(null);
      setLoading(true);
      setError(null);
      lastSessionRef.current = sessionId;
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

    // First load on mount / session change: fire immediately, no
    // debounce. dirtyTick changes during the same session use the
    // debounce.
    if (data === null) {
      fetchNow();
    } else {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
      }
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        fetchNow();
      }, REFETCH_DEBOUNCE_MS);
    }

    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
      abortRef.current?.abort();
    };
    // We intentionally don't include `data` here — including it would
    // re-trigger the effect on every successful fetch and cause a
    // refetch loop. dirtyTick is the explicit "you should refetch"
    // signal; sessionId / enabled are the other reasons to (re-)init.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled, dirtyTick, getSessionChanges]);

  return { data, loading, error };
}
