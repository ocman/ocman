import { useSyncExternalStore } from 'react';
import { hashKey, type QueryKey } from '@tanstack/react-query';
import type { Session } from './api';
import { queryClient } from './queryClient';
import { useApiStore } from './apiStore';
import { compareSidebarCompletion, computeSidebarHash } from './sidebarHelpers';
import { insertProvisionalSession } from './queries';

/**
 * The session list lives in one place: the react-query `['sessions', …]`
 * key family. Server lists (Dashboard, project pages, the palette, …) use
 * `useSessions` params as the second segment; the sidebar's merged list,
 * which keeps SSE-derived optimistic fields across polls, is the
 * `SIDEBAR_SESSIONS_KEY` entry. Optimistic patches go through
 * `patchSessionLists`, so every view sees them.
 */
export const SESSIONS_KEY = ['sessions'] as const;
export const SIDEBAR_SESSIONS_KEY = ['sessions', { view: 'sidebar' }] as const;

const EMPTY: Session[] = [];

// The sidebar list is client-merged state, not a fetch: it never refetches
// on its own and survives navigation away from the sidebar.
queryClient.setQueryDefaults(SIDEBAR_SESSIONS_KEY, { staleTime: Infinity, gcTime: Infinity });

export function isSidebarSessionsKey(key: QueryKey): boolean {
  return (key[1] as { view?: string } | undefined)?.view === 'sidebar';
}

export function getSidebarSessions(): Session[] {
  return queryClient.getQueryData<Session[]>(SIDEBAR_SESSIONS_KEY) ?? EMPTY;
}

const sidebarHash = hashKey(SIDEBAR_SESSIONS_KEY);
function subscribeSidebar(onChange: () => void) {
  // Query cache events fire synchronously, unlike observer notifications
  // (batched to a later tick), so a flushSync'd write renders at once.
  return queryClient.getQueryCache().subscribe((event) => {
    if (event.query.queryHash === sidebarHash) onChange();
  });
}

/** The sidebar's merged session list. */
export function useSidebarSessionList(): Session[] {
  return useSyncExternalStore(subscribeSidebar, getSidebarSessions);
}

/**
 * Replace the sidebar list (from the poll, archive, or tests). Rows whose
 * interruption was read while the request ran stay read; identical lists
 * are skipped so memoised project groups don't rebuild.
 */
export function setSidebarSessions(sessions: Session[]): void {
  const store = useApiStore.getState();
  const reads = store.pendingInterruptionReads;
  const rows = sessions.map(s => {
    const read = reads[`${s.platform}:${s.id}`];
    if (!read || s.status !== 'interrupted' || s.timeUpdated > read.timeUpdated || (s.seen && s.seenTimeUpdated >= read.timeUpdated)) return s;
    return { ...s, seen: true, seenTimeUpdated: Math.max(s.seenTimeUpdated, read.timeUpdated) };
  });
  const current = queryClient.getQueryData<Session[]>(SIDEBAR_SESSIONS_KEY);
  if (current && computeSidebarHash(current) === computeSidebarHash(rows)) return;
  // A completed cold-load read stays pending until its sidebar row exists.
  const pendingInterruptionReads = Object.fromEntries(Object.entries(reads)
    .filter(([key, read]) => !read.confirmed || !sessions.some(s => `${s.platform}:${s.id}` === key)));
  useApiStore.setState({ pendingInterruptionReads });
  queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, rows);
}

/**
 * Optimistically merge `patch` into every cached copy of a session. The
 * sidebar list is re-sorted, and a status change bumps the row's status
 * revision so an in-flight poll doesn't overwrite it.
 */
export function patchSessionLists(id: string, patch: Partial<Session>, platform?: string): void {
  const matches = (s: Session) => s.id === id && (!platform || s.platform === platform);
  const sidebarRow = getSidebarSessions().find(matches);
  for (const [key, rows] of queryClient.getQueriesData<Session[]>({ queryKey: SESSIONS_KEY })) {
    if (!Array.isArray(rows) || !rows.some(matches)) continue;
    const next = rows.map(s => matches(s) ? { ...s, ...patch } : s);
    if (isSidebarSessionsKey(key)) next.sort(compareSidebarCompletion);
    queryClient.setQueryData(key, next);
  }
  if (sidebarRow && patch.status !== undefined) {
    const rev = `${sidebarRow.platform}:${id}`;
    useApiStore.setState(state => ({ recentStatusRevisions: { ...state.recentStatusRevisions, [rev]: (state.recentStatusRevisions[rev] ?? 0) + 1 } }));
  }
}

/**
 * Show a brand-new session everywhere at once: every loaded server list
 * (see insertProvisionalSession) and the top of the sidebar. The next
 * poll reconciles the stub with the real row.
 */
export function seedSessionLists(session: Session): void {
  insertProvisionalSession(queryClient, session);
  queryClient.setQueryData<Session[]>(SIDEBAR_SESSIONS_KEY, (rows = EMPTY) => [session, ...rows.filter((s) => s.id !== session.id)]);
}
