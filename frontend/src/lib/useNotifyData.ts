import { useQuery } from '@tanstack/react-query';
import { api, type NotifyEntry, type NotifyPrompt } from './api';
import { useActivityScope } from './activityScopes';
import { coalescedRefresh, withDeadline } from './coalescedRefresh';
import { queryClient } from './queryClient';

/**
 * Shared `/api/sessions/notify` query behind the favicon, badge, bell,
 * OS-notification and toast hooks. React Query dedupes the consumers into
 * one request per cycle, polls only while the tab is visible and refetches
 * when it becomes visible again. Requests are never aborted.
 *
 * See spec/ui-responsiveness P2.
 */

const POLL_INTERVAL_MS = 10_000;
const LOOKBACK_MS = 7 * 24 * 60 * 60 * 1000;
const LIMIT = 500;

// Not under ['sessions']: those caches hold session lists and are patched and
// invalidated as a family.
export const notifyQueryKey = ['notify'] as const;

export type NotifySnapshot = {
  entries: NotifyEntry[];
  /** Start time of the request, for notification freshness. */
  fetchedAt: number;
};

export type ResolvedPrompt = NotifyPrompt & { kind: 'permission' | 'question' };

function promptKey(prompt: NotifyPrompt, kind: ResolvedPrompt['kind']): string {
  return JSON.stringify([prompt.platform, prompt.sessionId, prompt.requestId, kind]);
}

/** Trailing window that folds a burst of resolve events into one fetch. */
export const NOTIFY_RECHECK_DELAY_MS = 150;
/** Bounds one request so a stalled fetch cannot block every later refresh. */
export const NOTIFY_TIMEOUT_MS = 20_000;

// Prompt resolutions are numbered; `resolvedAt` maps a prompt to the
// number of its latest one, so a response can tell which resolutions it
// may predate.
let resolutions = 0;
const resolvedAt = new Map<string, number>();

async function fetchNotify(): Promise<NotifySnapshot> {
  const startedAfter = resolutions;
  const fetchedAt = Date.now();
  // RQ's signal is deliberately unused so unmounting never aborts a request.
  const data = await withDeadline(NOTIFY_TIMEOUT_MS, (signal) =>
    api.sessionsNotify({ since: Date.now() - LOOKBACK_MS, limit: LIMIT, includeDone: 1 }, signal));
  // Reconcile by owner, prompting session and request identity, rather than
  // the visible ancestor's id. Other outstanding prompts remain visible.
  const entries = data.map((entry) => {
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
    (!entry.seen && !entry.suppressTerminal && ['waiting', 'error', 'interrupted'].includes(entry.status)));
  for (const [id, at] of resolvedAt) if (at <= startedAfter) resolvedAt.delete(id);
  return { entries, fetchedAt };
}

function hasConsumers(): boolean {
  return (queryClient.getQueryCache().find({ queryKey: notifyQueryKey })?.getObserversCount() ?? 0) > 0;
}

// A recheck must reflect state after the event: an in-flight request may
// predate it, so wait for that one and fetch again instead of aborting it.
// Rechecks run while the tab is hidden; only polling pauses.
const recheckRunner = coalescedRefresh(async () => {
  const filters = { queryKey: notifyQueryKey, type: 'active' } as const;
  if (queryClient.isFetching({ queryKey: notifyQueryKey })) {
    await queryClient.refetchQueries(filters, { cancelRefetch: false });
  }
  await queryClient.refetchQueries(filters, { cancelRefetch: false });
}, NOTIFY_RECHECK_DELAY_MS);

/** Latest notify snapshot, polled while any consumer is mounted. */
export function useNotifySnapshot(): NotifySnapshot | undefined {
  useActivityScope('sessions');
  return useQuery({
    queryKey: notifyQueryKey,
    queryFn: fetchNotify,
    staleTime: 0,
    retry: false,
    refetchInterval: POLL_INTERVAL_MS,
    refetchOnReconnect: false,
    // Each poll is a new snapshot, so consumers re-evaluate it even when
    // the payload is unchanged.
    structuralSharing: false,
  }, queryClient).data;
}

/** Latest notify entries, or null before the first successful fetch. */
export function useNotifyData(): NotifyEntry[] | null {
  return useNotifySnapshot()?.entries ?? null;
}

/**
 * Force a refetch soon (e.g. after marking a session seen). Pass the
 * complete identity of the prompt just resolved, if any.
 */
export function recheckNotifyData(resolved?: ResolvedPrompt) {
  if (!hasConsumers()) return;
  if (resolved) resolvedAt.set(promptKey(resolved, resolved.kind), ++resolutions);
  recheckRunner.schedule();
}

/**
 * Reset internal state for tests. Not part of the public API.
 */
export function __resetForTests() {
  recheckRunner.reset();
  resolvedAt.clear();
  queryClient.removeQueries({ queryKey: notifyQueryKey });
}
