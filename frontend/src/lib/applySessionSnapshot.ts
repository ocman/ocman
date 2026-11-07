import { hashKey, type QueryClient, type QueryKey } from '@tanstack/react-query';
import type { Session } from './api';
import { isSidebarSessionsKey } from './sessionsCache';
import { eventRefresh } from './eventRefresh';

type ListScope = { dir?: string; sinceHours?: number; limit?: number; accounting?: boolean };
const reads = new Set<{ hash: string; changed: Set<string> }>();
const identity = (row: Session) => JSON.stringify([row.platform, row.id]);
const order = (a: Session, b: Session) => b.timeUpdated - a.timeUpdated || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

function matchesScope(session: Session, scope: ListScope): boolean {
  if (scope.dir && scope.dir !== session.directory) return false;
  if (!scope.sinceHours || !session.archived) return true;
  return session.timeUpdated >= Date.now() - scope.sinceHours * 3_600_000 ||
    session.status === 'busy' || !!session.pinned || (session.snoozedUntil ?? 0) > 0 || !!session.pendingPermission || !!session.pendingQuestion;
}

export function createCompactSessionRefresh(client: QueryClient) {
  const pending = new Set<string>();
  const refresh = eventRefresh(async () => {
    const hashes = new Set(pending);
    pending.clear();
    const filters = { queryKey: ['sessions'], predicate: (query: { queryHash: string }) => hashes.has(query.queryHash) };
    await Promise.all(client.getQueryCache().findAll(filters).map(query => query.promise?.catch(() => {})));
    await client.invalidateQueries(filters, { cancelRefetch: false });
  });
  return (session: Session) => {
    const applied = applySessionSnapshot(client, session);
    for (const query of client.getQueryCache().findAll({ queryKey: ['sessions'] })) {
      if (!isSidebarSessionsKey(query.queryKey) && !applied.has(query.queryHash)) pending.add(query.queryHash);
    }
    if (pending.size) refresh.schedule();
  };
}

/** Bounded pages can update an existing row that stays eligible and only moves
 * forward. Unknown rows, removals and backwards moves need replacement. */
export function applySessionSnapshot(client: QueryClient, session: Session): Set<string> {
  const applied = new Set<string>();
  for (const query of client.getQueryCache().findAll({ queryKey: ['sessions'] })) {
    const key: QueryKey = query.queryKey;
    const scope = (key[1] as ListScope | undefined) ?? {};
    const rows = query.state.data as Session[] | undefined;
    if (isSidebarSessionsKey(key) || !Array.isArray(rows) || scope.accounting || session.sourceChild) continue;
    const matches = matchesScope(session, scope);
    const existing = rows.find(row => identity(row) === identity(session));
    if (!existing || existing.sourceChild) continue;
    if (scope.limit !== 0 && (!matches || session.timeUpdated < existing.timeUpdated)) continue;
    const next = rows.flatMap(row => identity(row) !== identity(session) ? [row] : matches ? [session] : []);
    next.sort(order);
    client.setQueryData(key, next);
    applied.add(query.queryHash);
    for (const read of reads) if (read.hash === query.queryHash) read.changed.add(identity(session));
  }
  return applied;
}

export async function readSessionInventory(client: QueryClient, key: QueryKey, read: () => Promise<Session[] | null>, signal?: AbortSignal): Promise<Session[]> {
  const scope = key[1] as ListScope | undefined;
  if (scope?.accounting) return (await read()) ?? [];
  const fence = { hash: hashKey(key), changed: new Set<string>() };
  const release = () => reads.delete(fence);
  reads.add(fence);
  signal?.addEventListener('abort', release, { once: true });
  try {
    const incoming = (await read()) ?? [];
    if (fence.changed.size === 0) return incoming;
    const current = client.getQueryData<Session[]>(key) ?? [];
    const live = new Map(current.map(row => [identity(row), row]));
    const seen = new Set(incoming.map(identity));
    const next = incoming.flatMap(row => !fence.changed.has(identity(row)) ? [row] : live.has(identity(row)) ? [live.get(identity(row))!] : []);
    for (const row of current) if (!seen.has(identity(row)) && fence.changed.has(identity(row))) next.push(row);
    next.sort(order);
    return scope?.limit === 0 ? next : next.slice(0, scope?.limit ?? 500);
  } finally {
    release();
    signal?.removeEventListener('abort', release);
  }
}
