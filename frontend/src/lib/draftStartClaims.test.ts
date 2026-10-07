import { afterEach, expect, it, vi } from 'vitest';
import { claimDraftStart, persistDraftStart, type DraftStart } from './draftStartClaims';

afterEach(() => vi.unstubAllGlobals());

type Request = { result?: DraftStart; onsuccess?: () => void };
type Transaction = {
  oncomplete?: () => void;
  onabort?: () => void;
  error?: Error;
  objectStore: () => { get: (key: string) => Request; put: (value: DraftStart, key: string) => void };
};

// Unit tests drive the SDK callbacks; the two-tab browser regression verifies real transaction serialization.
function database(options: { manualCommit?: boolean; blocked?: boolean; openError?: Error; abort?: boolean; brokenSchema?: boolean } = {}) {
  const rows = new Map<string, DraftStart>();
  const staged = new Map<string, DraftStart>();
  let transaction: Transaction;
  const commit = () => {
    for (const [key, value] of staged) rows.set(key, value);
    staged.clear();
    transaction.oncomplete?.();
  };
  const db = {
    close: vi.fn(), createObjectStore: vi.fn(),
    transaction: () => {
      if (options.brokenSchema) throw new Error('schema missing');
      transaction = { objectStore: () => ({
        get: (key) => {
          const request: Request = {};
          queueMicrotask(() => {
            if (options.abort) { transaction.onabort?.(); return; }
            request.result = rows.get(key);
            request.onsuccess?.();
            if (!options.manualCommit) queueMicrotask(commit);
          });
          return request;
        },
        put: (value, key) => { staged.set(key, value); },
      }) };
      return transaction;
    },
  };
  vi.stubGlobal('indexedDB', { open: () => {
    const request = { result: db, error: options.openError,
      onsuccess: undefined as (() => void) | undefined, onerror: undefined as (() => void) | undefined,
      onblocked: undefined as (() => void) | undefined, onupgradeneeded: undefined as (() => void) | undefined };
    queueMicrotask(() => {
      if (options.openError) { request.onerror?.(); return; }
      if (options.blocked) request.onblocked?.();
      request.onupgradeneeded?.();
      request.onsuccess?.();
    });
    return request;
  } });
  return { rows, staged, commit, close: db.close };
}

it('does not grant a claim until the transaction commits', async () => {
  const db = database({ manualCommit: true });
  let resolved = false;
  const result = claimDraftStart('draft', { version: 1, text: 'prompt' }).then((value) => { resolved = true; return value; });
  await vi.waitFor(() => expect(db.staged.has('draft')).toBe(true));
  expect(resolved).toBe(false);
  db.commit();
  expect(await result).toEqual({ claimed: true, start: { version: 1, text: 'prompt' } });
  expect(db.close).toHaveBeenCalledOnce();
});

it('preserves a standing claim, permits an explicit failed-start retry and retains completion', async () => {
  database();
  const original = { version: 1, text: 'first' };
  expect((await claimDraftStart('draft', original)).claimed).toBe(true);
  expect(await claimDraftStart('draft', { version: 1, text: 'duplicate' })).toEqual({ claimed: false, start: original });
  await persistDraftStart('draft', { ...original, error: 'failed' });
  expect((await claimDraftStart('draft', original)).claimed).toBe(true);
  await persistDraftStart('draft', { ...original, sessionId: 'session' });
  expect((await claimDraftStart('draft', original)).start.sessionId).toBe('session');
});

it('fails closed without IndexedDB', async () => {
  vi.stubGlobal('indexedDB', undefined);
  await expect(claimDraftStart('draft', { version: 0, text: 'prompt' })).rejects.toThrow('cannot coordinate');
});

it.each([
  [{ openError: new Error('open failed') }, 'open failed'],
  [{ blocked: true }, 'Close other ocman tabs'],
  [{ brokenSchema: true }, 'schema missing'],
  [{ abort: true }, 'Could not save'],
] as const)('does not grant a claim after an SDK failure %j', async (options, message) => {
  const db = database(options);
  await expect(claimDraftStart('draft', { version: 0, text: 'prompt' })).rejects.toThrow(message);
  expect(db.rows.size).toBe(0);
});
