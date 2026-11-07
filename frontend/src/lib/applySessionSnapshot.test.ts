import { afterEach, describe, expect, it, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import { applySessionSnapshot, createCompactSessionRefresh, readSessionInventory } from './applySessionSnapshot';
import type { Session } from './api';

const row = { id: 'one', platform: 'local', directory: '/repo', timeUpdated: Date.now(), archived: false } as Session;
const client = new QueryClient();
afterEach(() => { client.clear(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe('compact rows shared across session lists', () => {
  it('keeps unknown-row reconciliation pending through a burst of known-row updates', async () => {
    vi.useFakeTimers();
    const key = ['sessions', { limit: 50 }];
    client.setQueryData(key, [row]);
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const refresh = createCompactSessionRefresh(client);
    refresh({ ...row, id: 'unknown' });
    refresh({ ...row, title: 'known update' });
    await vi.advanceTimersByTimeAsync(150);
    expect(invalidate).toHaveBeenCalledOnce();
    const query = client.getQueryCache().find({ queryKey: key })!;
    expect(invalidate.mock.calls[0][0]?.predicate?.(query)).toBe(true);
    refresh({ ...row, title: 'another known update' });
    await vi.advanceTimersByTimeAsync(150);
    expect(invalidate).toHaveBeenCalledOnce();
  });
  it('updates and orders complete inventories without crossing owners or inventing membership', () => {
    const key = ['sessions', { limit: 0 }];
    client.setQueryData(key, [row, { ...row, platform: 'remote' }]);
    const changed = { ...row, title: 'updated', timeUpdated: row.timeUpdated + 1 };
    expect(applySessionSnapshot(client, changed).size).toBe(1);
    expect(client.getQueryData(key)).toEqual([changed, { ...row, platform: 'remote' }]);
    expect(applySessionSnapshot(client, { ...row, id: 'two', timeUpdated: row.timeUpdated + 2 }).size).toBe(0);
    expect(client.getQueryData<Session[]>(key)?.[0].id).toBe('one');
  });

  it('preserves authoritative replacement for bounded membership changes and accounting lists', () => {
    for (const scope of [{ limit: 50 }, { limit: 0, accounting: true }]) client.setQueryData(['sessions', scope], [row]);
    expect(applySessionSnapshot(client, { ...row, id: 'unknown', title: 'updated' }).size).toBe(0);
    expect(client.getQueryData<Session[]>(['sessions', { limit: 50 }])?.[0].title).toBeUndefined();
  });

  it('updates existing bounded rows without refetching or changing page size', () => {
    const key = ['sessions', { limit: 1 }];
    client.setQueryData(key, [row]);
    const changed = { ...row, title: 'updated', timeUpdated: row.timeUpdated + 1 };
    expect(applySessionSnapshot(client, changed).size).toBe(1);
    expect(client.getQueryData(key)).toEqual([changed]);
    expect(applySessionSnapshot(client, { ...row, timeUpdated: 1 }).size).toBe(0);
  });

  it('respects directories and archived cutoffs while keeping attention rows', () => {
    const key = ['sessions', { dir: '/repo', sinceHours: 1, limit: 0 }];
    client.setQueryData(key, [row]);
    applySessionSnapshot(client, { ...row, directory: '/other' });
    expect(client.getQueryData(key)).toEqual([]);
    const old = { ...row, archived: true, timeUpdated: 1 };
    applySessionSnapshot(client, old);
    expect(client.getQueryData(key)).toEqual([]);
    applySessionSnapshot(client, { ...old, pendingQuestion: true });
    expect(client.getQueryData(key)).toEqual([]); // Unknown membership needs an authoritative read.
  });

  it('does not let an older HTTP response rewind a compact update', async () => {
    const key = ['sessions', { limit: 0 }];
    client.setQueryData(key, [row]);
    let resolve!: (rows: Session[]) => void;
    const response = readSessionInventory(client, key, () => new Promise(done => { resolve = done; }));
    const changed = { ...row, title: 'new', timeUpdated: row.timeUpdated + 1 };
    applySessionSnapshot(client, changed);
    resolve([row]);
    expect(await response).toEqual([changed]);
  });
});
