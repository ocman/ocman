import { useEffect } from 'react';
import { create } from 'zustand';
import { api, type NotifyEntry, type NotifyPrompt } from './api';
import { acquireActivityScope } from './activityScopes';
import { coalescedRefresh, withDeadline } from './coalescedRefresh';

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
  /** Pass the complete identity of the prompt just resolved, if any. */
  recheck: (resolved?: ResolvedPrompt) => void;
};

export type ResolvedPrompt = NotifyPrompt & { kind: 'permission' | 'question' };

function promptKey(prompt: NotifyPrompt, kind: ResolvedPrompt['kind']): string {
  return JSON.stringify([prompt.platform, prompt.sessionId, prompt.requestId, kind]);
}

/** Trailing window that folds a burst of resolve events into one fetch. */
export const NOTIFY_RECHECK_DELAY_MS = 150;
/** Bounds one request so a stalled fetch cannot block every later refresh. */
export const NOTIFY_TIMEOUT_MS = 20_000;

let intervalId: ReturnType<typeof setInterval> | null = null;
// Prompt resolutions are numbered; `resolvedAt` maps a session to the
// number of its latest one, so a response can tell which resolutions it
// may predate.
let resolutions = 0;
const resolvedAt = new Map<string, number>();
let releaseActivityScope: (() => void) | null = null;

// One request at a time, never aborted: a recheck during a fetch queues
// one follow-up instead of cancelling it (the old abort produced ~320
// client-cancelled requests a day). Errors are ignored; the next poll
// retries and consumers keep the last known `data`.
const fetcher = coalescedRefresh(async () => {
  // A queued follow-up may fire after the last consumer left. Event-driven
  // rechecks still run while the tab is hidden; only polling pauses.
  if (useNotifyStore.getState().refCount === 0) return;
  const startedAfter = resolutions;
  const data = await withDeadline(NOTIFY_TIMEOUT_MS, (signal) =>
    api.sessionsNotify({ since: Date.now() - LOOKBACK_MS, limit: LIMIT }, signal));
  // Reconcile by owner, prompting session and request identity, rather than
  // the visible ancestor's id. Other outstanding prompts remain visible.
  useNotifyStore.setState({
    data: data.map((entry) => {
      const keep = (kind: ResolvedPrompt['kind']) => (prompt: NotifyPrompt) =>
        (resolvedAt.get(promptKey(prompt, kind)) ?? 0) <= startedAfter;
      const permissions = entry.permissions?.filter(keep('permission'));
      const questions = entry.questions?.filter(keep('question'));
      return {
        ...entry,
        ...(permissions && { permissions, pendingPermission: permissions.length > 0 }),
        ...(questions && { questions, pendingQuestion: questions.length > 0 }),
      };
    }).filter((entry) => entry.pendingPermission || entry.pendingQuestion ||
      (!entry.seen && !entry.suppressTerminal && ['waiting', 'error', 'interrupted'].includes(entry.status))),
    lastFetched: Date.now(),
  });
  for (const [id, at] of resolvedAt) if (at <= startedAfter) resolvedAt.delete(id);
}, NOTIFY_RECHECK_DELAY_MS);

function startPolling() {
  if (intervalId !== null) return;
  // Immediate fetch on start.
  fetcher.now();
  intervalId = setInterval(fetcher.now, POLL_INTERVAL_MS);
}

function stopPolling() {
  if (intervalId !== null) {
    clearInterval(intervalId);
    intervalId = null;
  }
}

function onVisibilityChange() {
  if (document.hidden) {
    // Tab hidden — stop polling to save resources.
    stopPolling();
  } else {
    // Tab visible — resume polling with an immediate fetch.
    startPolling();
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
        startPolling();
      }
      visibilityHandler = onVisibilityChange;
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

  recheck: (resolved) => {
    if (useNotifyStore.getState().refCount > 0) {
      if (resolved) resolvedAt.set(promptKey(resolved, resolved.kind), ++resolutions);
      fetcher.schedule();
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
export function recheckNotifyData(resolved?: ResolvedPrompt) {
  useNotifyStore.getState().recheck(resolved);
}

/**
 * Reset internal state for tests. Not part of the public API.
 */
export function __resetForTests() {
  releaseActivityScope?.();
  releaseActivityScope = null;
  stopPolling();
  fetcher.reset();
  resolvedAt.clear();
  if (visibilityHandler) {
    document.removeEventListener('visibilitychange', visibilityHandler);
    visibilityHandler = null;
  }
  useNotifyStore.setState({ data: null, lastFetched: 0, refCount: 0 });
}
