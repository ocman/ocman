import { useCallback, useEffect, useRef, useState } from 'react';
import { useApiStore } from './apiStore';
import type { SessionInfo } from './api';
import { DebouncedTrigger } from './debouncedTrigger';

// Same debounce/ceiling values as useSessionChanges. Token counts
// move with every assistant turn, but the panel doesn't need to be
// any twitchier than the file-changes panel — both ride the same
// dirtyTick stream.
const REFETCH_DEBOUNCE_MS = 500;
const REFETCH_MAX_WAIT_MS = 5000;

// Stable empty payload returned when the hook is disabled or the
// platform doesn't expose this data. Reused across renders so
// referential equality holds; downstream effects keying off `data`
// won't churn.
const EMPTY_INFO: SessionInfo = {
  sessionId: '',
  supported: false,
  context: { tokens: 0, cost: 0, estCost: 0 },
  tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  mcpServers: [],
  lspServers: [],
  messages: { user: 0, assistant: 0 },
};

export interface UseSessionInfoResult {
  data: SessionInfo | null;
  loading: boolean;
  error: string | null;
  /**
   * Manually trigger an immediate refetch, bypassing the debounce.
   * Wired to the user-facing "Refresh" button. Safe to call when
   * `enabled` is false (no-op).
   */
  refresh: () => void;
}

export interface UseSessionInfoOptions {
  /**
   * When false, the hook returns synchronously with EMPTY_INFO and
   * never fires a request. Use this to skip the call when the
   * platform has sessionInfo=false (no point asking).
   */
  enabled?: boolean;
  /**
   * Tick that re-triggers a debounced fetch when its value increments.
   * The session-detail page increments this whenever an edit/write
   * part arrives via SSE; the same signal drives the file-changes
   * sidebar so both panels refresh in lockstep.
   */
  dirtyTick?: number;
}

/**
 * useSessionInfo fetches /api/session/{id}/info and re-fetches
 * whenever `options.dirtyTick` changes. Refetch timing follows
 * DebouncedTrigger: a 500 ms inner debounce that resets on every
 * tick, with a 5 s hard ceiling so a continuously-busy session still
 * sees periodic updates.
 *
 * Returns `{ data, loading, error, refresh }`. When `enabled` is
 * false, returns a synchronous EMPTY_INFO with `loading: false` so
 * the caller can render a "Not supported" empty state without a
 * flash of skeleton.
 *
 * Mirrors useSessionChanges shape so RightPanel can treat both panes
 * uniformly. The two hooks deliberately don't share a base — their
 * data shapes are different and the divergent specialisations stay
 * cleaner as siblings.
 */
export function useSessionInfo(
  sessionId: string | undefined,
  { enabled = true, dirtyTick = 0 }: UseSessionInfoOptions = {},
): UseSessionInfoResult {
  const [data, setData] = useState<SessionInfo | null>(null);
  const [loading, setLoading] = useState(enabled && !!sessionId);
  const [error, setError] = useState<string | null>(null);

  const getSessionInfo = useApiStore((s) => s.getSessionInfo);

  const lastSessionRef = useRef<string | undefined>(undefined);
  const abortRef = useRef<AbortController | null>(null);
  const fetchRef = useRef<() => void>(() => {});
  const triggerRef = useRef<DebouncedTrigger | null>(null);
  if (triggerRef.current === null) {
    triggerRef.current = new DebouncedTrigger(
      () => fetchRef.current(),
      { debounceMs: REFETCH_DEBOUNCE_MS, maxWaitMs: REFETCH_MAX_WAIT_MS },
    );
  }

  useEffect(() => {
    if (!enabled || !sessionId) {
      setData(enabled ? null : EMPTY_INFO);
      setLoading(false);
      setError(null);
      triggerRef.current?.reset();
      return;
    }

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

      getSessionInfo(sessionId, controller.signal)
        .then((resp) => {
          if (controller.signal.aborted) return;
          setData(resp);
          setLoading(false);
          setError(null);
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof Error && err.name === 'AbortError') return;
          const message = err instanceof Error ? err.message : 'Failed to load session info';
          setError(message);
          setLoading(false);
        });
    };
    fetchRef.current = fetchNow;

    if (data === null) {
      fetchNow();
    } else {
      triggerRef.current?.bump();
    }

    return () => {
      abortRef.current?.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled, dirtyTick, getSessionInfo]);

  useEffect(() => {
    return () => {
      triggerRef.current?.cancel();
    };
  }, []);

  const refresh = useCallback(() => {
    if (!enabled || !sessionId) return;
    triggerRef.current?.flushNow();
  }, [enabled, sessionId]);

  return { data, loading, error, refresh };
}
