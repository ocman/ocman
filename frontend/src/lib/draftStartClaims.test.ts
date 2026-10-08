import { expect, it, vi } from 'vitest';
import { claimDraftStart, persistDraftStart, readDraftStart } from './draftStartClaims';
import { closeDraftDbForTests, transact } from './draftDb';

it('preserves a standing claim, permits an explicit failed-start retry and retains completion', async () => {
  const original = { version: 1, text: '', attemptId: 'a' };
  expect((await claimDraftStart('draft', original)).claimed).toBe(true);
  expect(await claimDraftStart('draft', { version: 1, text: '', attemptId: 'b' })).toEqual({ claimed: false, start: original });
  await persistDraftStart('draft', { ...original, error: 'failed' });
  expect((await claimDraftStart('draft', { ...original, attemptId: 'c' })).claimed).toBe(true);
  await persistDraftStart('draft', { ...original, attemptId: 'c', sessionId: 'session' });
  expect((await claimDraftStart('draft', original)).start.sessionId).toBe('session');
  expect((await readDraftStart('draft'))?.sessionId).toBe('session');
  expect(await readDraftStart('missing')).toBeUndefined();
});

it('lets only one of two concurrent claims win', async () => {
  const results = await Promise.all([
    claimDraftStart('race', { version: 0, text: '', attemptId: 'first' }),
    claimDraftStart('race', { version: 0, text: '', attemptId: 'second' }),
  ]);
  expect(results.filter((result) => result.claimed)).toHaveLength(1);
});

it('does not overwrite a newer attempt with an older terminal outcome', async () => {
  const current = { version: 0, text: 'new attempt', attemptId: 'new' };
  await claimDraftStart('draft', current);
  expect(await persistDraftStart('draft', { version: 0, text: 'old attempt', attemptId: 'old', error: 'old failure' })).toEqual(current);
  expect(await readDraftStart('draft')).toEqual(current);
});

it('does not let a retry claim a released delivery that retained its old error', async () => {
  const failed = { version: 0, text: 'abandoned command', attemptId: 'released', deliveryState: 'failed' as const, error: 'failed' };
  await claimDraftStart('first-delivery:released', failed);
  await persistDraftStart('first-delivery:released', { ...failed, deliveryState: 'done', text: '' });
  await persistDraftStart('first-delivery:released', { ...failed, error: 'late error' });
  const retry = await claimDraftStart('first-delivery:released', { version: 0, text: 'abandoned command', attemptId: 'retry', deliveryState: 'pending' });
  expect(retry.claimed).toBe(false);
  expect(retry.start).toMatchObject({ attemptId: 'released', deliveryState: 'done' });
});

it('fails closed without IndexedDB', async () => {
  closeDraftDbForTests();
  vi.stubGlobal('indexedDB', undefined);
  try { await expect(claimDraftStart('draft', { version: 0, text: '' })).rejects.toThrow('cannot coordinate'); }
  finally { vi.unstubAllGlobals(); }
});

it('rejects a database whose stores are missing instead of granting a claim', async () => {
  closeDraftDbForTests();
  await new Promise<void>((resolve, reject) => {
    const open = indexedDB.open('ocman.drafts.v1', 1);
    open.onupgradeneeded = () => undefined; // An older/foreign schema: no stores.
    open.onsuccess = () => { open.result.close(); resolve(); };
    open.onerror = () => reject(open.error);
  });
  await expect(claimDraftStart('draft', { version: 0, text: '' })).rejects.toThrow();
});

it('aborts every write when the transaction body throws', async () => {
  await expect(transact(['starts'], 'readwrite', (tx) => {
    tx.put('starts', 'partial', { version: 0, text: '' });
    throw new Error('boom');
  })).rejects.toThrow('boom');
  expect(await readDraftStart('partial')).toBeUndefined();
});

it('reopens a connection the browser closed', async () => {
  await readDraftStart('warm');
  const close = vi.spyOn(IDBDatabase.prototype, 'transaction').mockImplementationOnce(() => { throw new DOMException('closed', 'InvalidStateError'); });
  try { expect((await claimDraftStart('reopened', { version: 0, text: '' })).claimed).toBe(true); } finally { close.mockRestore(); }
  expect(await readDraftStart('reopened')).toEqual({ version: 0, text: '' });
});
