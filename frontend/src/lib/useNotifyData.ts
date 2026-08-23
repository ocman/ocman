import { useEffect } from 'react';
import { create } from 'zustand';
import { api, type NotifyEntry } from './api';
import { acquireActivityScope } from './activityScopes';

/**
 * Shared notify-data store that coalesces the four independent
 * `/api/sessions/notify` pollers (favicon, bell, OS notification, toast)
 * into a single request per cycle.
 *
 * Each consumer calls `subscribe()` on mount and `unsubscribe()` on
 * unmount. Polling starts when the first consumer subscribes and stops
 * when the last one unsubscribes. The result fans out to all consumers
 * via the Zustand store.
 *
 * Polling pauses while `document.hidden` and resumes with an immediate
 * refetch on `visibilitychange`.
 *
 * See spec/ui-responsiveness P2.
 */

const POLL_INTERVAL_MS = 10_000;
const LOOKBACK_MS = 7 * 24 * 60 * 60 * 1000;
const LIMIT = 500;

type NotifyDataState = {
  /** Latest payload from the server, or null if never fetched. */
  data: NotifyEntry[] | null;
  /** Epoch ms of the last successful fetch. */
  lastFetched: number;
  /** Number of active consumers. */
  refCount: number;
  /** Subscribe a consumer — starts polling if first. */
  subscribe: () => void;
  /** Unsubscribe a consumer — stops polling if last. */
  unsubscribe: () => void;
  /** Force an immediate refetch (e.g. after marking a session seen). */
  recheck: () => void;
};

let intervalId: ReturnType<typeof setInterval> | null = null;
let abortController: AbortController | null = null;
let releaseActivityScope: (() => void) | null = null;

async function fetchNotify(set: (partial: Partial<NotifyDataState>) => void) {
  // Abort any in-flight request so we never have two concurrent fetches.
  abortController?.abort();
  abortController = new AbortController();
  try {
    const data = await api.sessionsNotify(
      { since: Date.now() - LOOKBACK_MS, limit: LIMIT },
      abortController.signal,
    );
    set({ data, lastFetched: Date.now() });
  } catch {
    // Network errors and aborts are silently ignored — the next poll
    // will retry. We intentionally don't clear `data` so consumers
    // keep rendering the last known state.
  }
}

function startPolling(set: (partial: Partial<NotifyDataState>) => void) {
  if (intervalId !== null) return;
  // Immediate fetch on start.
  void fetchNotify(set);
  intervalId = setInterval(() => void fetchNotify(set), POLL_INTERVAL_MS);
}

function stopPolling() {
  if (intervalId !== null) {
    clearInterval(intervalId);
    intervalId = null;
  }
  abortController?.abort();
  abortController = null;
}

function onVisibilityChange(set: (partial: Partial<NotifyDataState>) => void) {
  if (document.hidden) {
    // Tab hidden — stop polling to save resources.
    stopPolling();
  } else {
    // Tab visible — resume polling with an immediate fetch.
    startPolling(set);
  }
}

// Module-level listener ref so we can add/remove it cleanly.
let visibilityHandler: (() => void) | null = null;

export const useNotifyStore = create<NotifyDataState>((set) => ({
  data: null,
  lastFetched: 0,
  refCount: 0,

  subscribe: () => {
    const next = useNotifyStore.getState().refCount + 1;
    set({ refCount: next });
    if (next === 1) {
      releaseActivityScope = acquireActivityScope('sessions');
      // First consumer — start polling and listen for visibility.
      if (!document.hidden) {
        startPolling(set);
      }
      visibilityHandler = () => onVisibilityChange(set);
      document.addEventListener('visibilitychange', visibilityHandler);
    }
  },

  unsubscribe: () => {
    const next = Math.max(0, useNotifyStore.getState().refCount - 1);
    set({ refCount: next });
    if (next === 0) {
      releaseActivityScope?.();
      releaseActivityScope = null;
      // Last consumer gone — stop everything.
      stopPolling();
      if (visibilityHandler) {
        document.removeEventListener('visibilitychange', visibilityHandler);
        visibilityHandler = null;
      }
    }
  },

  recheck: () => {
    if (useNotifyStore.getState().refCount > 0) {
      void fetchNotify(set);
    }
  },
}));

/**
 * Hook that subscribes to the shared notify data on mount and
 * unsubscribes on unmount. Returns the latest payload.
 */
export function useNotifyData(): NotifyEntry[] | null {
  const data = useNotifyStore((s) => s.data);

  useEffect(() => {
    useNotifyStore.getState().subscribe();
    return () => useNotifyStore.getState().unsubscribe();
  }, []);

  return data;
}

// Re-export for consumers that need to trigger a recheck (e.g. after
// marking a session seen).
export function recheckNotifyData() {
  useNotifyStore.getState().recheck();
}

/**
 * Reset internal state for tests. Not part of the public API.
 */
export function __resetForTests() {
  releaseActivityScope?.();
  releaseActivityScope = null;
  stopPolling();
  if (visibilityHandler) {
    document.removeEventListener('visibilitychange', visibilityHandler);
    visibilityHandler = null;
  }
  useNotifyStore.setState({ data: null, lastFetched: 0, refCount: 0 });
}
