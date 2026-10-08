/**
 * One IndexedDB database for browser-local drafts: composer text, prepared
 * conversation metadata and start/delivery receipts. Every multi-key change is
 * a single readwrite transaction, so tabs can never observe or interleave a
 * half-applied relocation, retirement or claim. IndexedDB runs overlapping
 * readwrite transactions one at a time in creation order, which is what makes
 * revision checks and their writes atomic across tabs.
 */
export type DraftStoreName = 'texts' | 'drafts' | 'starts';
const STORES: DraftStoreName[] = ['texts', 'drafts', 'starts'];
const NAME = 'ocman.drafts.v1';

export interface DraftTx {
  get<T>(store: DraftStoreName, key: string): Promise<T | undefined>;
  getAll<T>(store: DraftStoreName): Promise<[string, T][]>;
  put(store: DraftStoreName, key: string, value: unknown): void;
  delete(store: DraftStoreName, key: string): void;
}

let connection: IDBDatabase | undefined;
let opening: Promise<IDBDatabase> | undefined;
// Tests replace the database between cases; work started before that never settles.
let epoch = 0;

export function openDraftDb(): Promise<IDBDatabase> {
  if (connection) return Promise.resolve(connection);
  if (opening) return opening;
  opening = new Promise<IDBDatabase>((resolve, reject) => {
    if (typeof indexedDB === 'undefined') { reject(new Error('This browser cannot coordinate session starts.')); return; }
    const open = indexedDB.open(NAME, 1);
    let blocked = false;
    open.onupgradeneeded = () => { for (const store of STORES) open.result.createObjectStore(store); };
    open.onerror = () => reject(open.error || new Error('Could not open the draft database'));
    open.onblocked = () => { blocked = true; reject(new Error('Close other ocman tabs to update the draft database')); };
    open.onsuccess = () => {
      const db = open.result;
      if (blocked) { db.close(); return; }
      // A future schema upgrade in another tab must not wait on this connection.
      db.onversionchange = () => { db.close(); if (connection === db) connection = undefined; };
      // The browser can close it too (site data cleared, storage error): reopen on next use.
      db.onclose = () => { if (connection === db) connection = undefined; };
      connection = db;
      resolve(db);
    };
  }).finally(() => { opening = undefined; });
  return opening;
}

function request<T>(req: IDBRequest<T>) {
  return new Promise<T>((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

/**
 * Run `body` inside one transaction; the result resolves only once it commits.
 * `body` may await only `tx.get`/`tx.getAll` (other awaits would let the
 * transaction auto-commit early). A thrown error aborts every write.
 */
export async function transact<T>(stores: DraftStoreName[], mode: IDBTransactionMode, body: (tx: DraftTx) => Promise<T> | T): Promise<T> {
  const started = epoch;
  const result = await run(stores, mode, body).catch((error) => { if (started !== epoch) return new Promise<never>(() => {}); throw error; });
  return started === epoch ? result : new Promise<never>(() => {});
}

async function run<T>(stores: DraftStoreName[], mode: IDBTransactionMode, body: (tx: DraftTx) => Promise<T> | T, retried = false): Promise<T> {
  const db = connection || await openDraftDb();
  let transaction: IDBTransaction;
  try { transaction = db.transaction(stores, mode); }
  catch (error) {
    // A closed connection is reopened once; a missing store is a real schema error.
    if (error instanceof DOMException && error.name === 'InvalidStateError' && !retried) {
      if (connection === db) connection = undefined;
      return run(stores, mode, body, true);
    }
    throw error;
  }
  const done = new Promise<void>((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error || new Error('Could not save the draft'));
    transaction.onerror = () => undefined; // Reported through onabort.
  });
  const tx: DraftTx = {
    get: (store, key) => request(transaction.objectStore(store).get(key)),
    getAll: async (store) => {
      const objectStore = transaction.objectStore(store);
      const [keys, values] = await Promise.all([request(objectStore.getAllKeys()), request(objectStore.getAll())]);
      return keys.map((key, index) => [String(key), values[index]]);
    },
    put: (store, key, value) => { transaction.objectStore(store).put(value, key); },
    delete: (store, key) => { transaction.objectStore(store).delete(key); },
  };
  let result: T;
  try { result = await body(tx); }
  catch (error) {
    try { transaction.abort(); } catch { /* Already finished. */ }
    await done.catch(() => undefined);
    throw error;
  }
  await done;
  return result;
}

/** Cross-tab change notification: peers re-read the named keys from the database. */
export type DraftChange = Partial<Record<DraftStoreName, string[]>>;
const channel = typeof BroadcastChannel === 'undefined' ? undefined : new BroadcastChannel('ocman.drafts.v1');
const listeners = new Set<(change: DraftChange) => void>();
// Node (tests) keeps a process alive for an open channel; browsers ignore this.
(channel as unknown as { unref?: () => void } | undefined)?.unref?.();
if (channel) channel.onmessage = (event: MessageEvent<DraftChange>) => { for (const listener of listeners) listener(event.data); };

export function publishDraftChange(change: DraftChange) { channel?.postMessage(change); }
export function onDraftChange(listener: (change: DraftChange) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** Tests only: drop the cached connection so a fresh fake database can be used. */
export function closeDraftDbForTests() {
  epoch++;
  connection?.close();
  connection = undefined;
  opening = undefined;
}
