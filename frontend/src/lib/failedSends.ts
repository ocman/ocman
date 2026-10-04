// Persistent storage of message sends that failed on the client.
//
// When `sendMessage` rejects (network down, 5xx, etc.) we lose the user's
// prompt. Persisting the failed send lets us:
//   1. Show a Retry button on the user's message bubble so they don't have to
//      retype the prompt.
//   2. Survive a page refresh — the prompt stays retryable until the user
//      explicitly retries or dismisses it.
//
// Complete payloads stay in memory for the page lifetime. A best-effort
// localStorage copy, scoped per session id, provides reload recovery.
// Image data URLs can be large (multi-MB base64), so per-entry storage is
// capped: when an entry exceeds the limit we drop the images but keep the
// text retryable, with `imagesDropped` flagging the loss for the UI.

const STORAGE_KEY = 'ocman.failedSends.v1';

// Per-entry size cap. localStorage typically allows ~5 MB per origin; we
// stay well under that so multiple entries can coexist.
const MAX_ENTRY_BYTES = 4 * 1024 * 1024;

export interface FailedSendImage {
  url: string;
  mime: string;
}

export interface FailedSend {
  /** Stable id used to reconcile the UI bubble with this entry. */
  id: string;
  text: string;
  images?: FailedSendImage[];
  /** True when images were stripped at persist time to fit the size cap. */
  imagesDropped?: boolean;
  /** Selections at the time of the original send; replayed on retry. */
  model?: string;
  agent?: string;
  reasoning?: string;
  /** Error message surfaced to the user. */
  error: string;
  /** Wall-clock ms; used purely for ordering / display. */
  failedAt: number;
}

type Store = Record<string, FailedSend[]>;

const live: Store = Object.create(null);
const listeners = new Set<(sessionId: string) => void>();

export function subscribeFailedSends(listener: (sessionId: string) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

function notify(sessionId: string) {
  for (const listener of listeners) listener(sessionId);
}

function loadStore(): Store {
  if (typeof window === 'undefined') return {};
  try {
    return parseStore(window.localStorage.getItem(STORAGE_KEY));
  } catch {
    return {};
  }
}

function saveStore(data: Store): boolean {
  if (typeof window === 'undefined') return false;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
    return true;
  } catch {
    // Quota exceeded / private mode / disabled storage. Nothing we can do
    // here; the shared in-memory state still reflects the failure
    // for the current page lifetime.
    return false;
  }
}

function entrySize(entry: FailedSend): number {
  // Cheap approximation — JSON byte length is dominated by base64 image
  // payloads in practice, and we only need an order-of-magnitude check.
  return JSON.stringify(entry).length;
}

/**
 * Strip images from an entry when it exceeds the per-entry size cap.
 * Mutation-free: returns the same entry when no change is needed, otherwise
 * returns a copy with `images` removed and `imagesDropped` set.
 */
function fitEntry(entry: FailedSend): FailedSend {
  if (entrySize(entry) <= MAX_ENTRY_BYTES) return entry;
  if (!entry.images || entry.images.length === 0) return entry;
  return { ...entry, images: undefined, imagesDropped: true };
}

// localStorage is shared by every tab, so it stays the list of record and
// is merged per entry id; only this page's own payloads and removals
// overlay it.
const key = (sessionId: string, id: string) => `${sessionId}\0${id}`;
// Entry keys this page removed: hidden even if their storage write failed.
const removed = new Set<string>();
// Entry keys this page persisted: absent from storage means another tab
// retried or dismissed them.
const synced = new Set<string>();

function persisted(store: Store, sessionId: string): FailedSend[] {
  const list = store[sessionId];
  return Array.isArray(list) ? list : [];
}

export function listFailedSends(sessionId: string): FailedSend[] {
  const mine = live[sessionId] ?? [];
  const stored = persisted(loadStore(), sessionId).filter((e) => !removed.has(key(sessionId, e.id)));
  const storedIds = new Set(stored.map((e) => e.id));
  // Prefer this page's complete payload over the capped stored copy.
  const merged = stored.map((e) => mine.find((m) => m.id === e.id) ?? e);
  const unpersisted = mine.filter((e) => !storedIds.has(e.id) && !synced.has(key(sessionId, e.id)));
  live[sessionId] = mine.filter((e) => storedIds.has(e.id) || !synced.has(key(sessionId, e.id)));
  return [...merged, ...unpersisted].sort((a, b) => a.failedAt - b.failedAt);
}

function writeSession(sessionId: string, list: FailedSend[]) {
  const store = loadStore();
  if (list.length === 0) delete store[sessionId];
  else store[sessionId] = list.map(fitEntry);
  if (!saveStore(store)) return;
  for (const e of list) synced.add(key(sessionId, e.id));
}

export function recordFailedSend(sessionId: string, entry: FailedSend) {
  removed.delete(key(sessionId, entry.id));
  synced.delete(key(sessionId, entry.id));
  live[sessionId] = [...(live[sessionId] ?? []).filter((e) => e.id !== entry.id), entry];
  writeSession(sessionId, listFailedSends(sessionId));
  notify(sessionId);
}

function forget(sessionId: string, ids: string[]) {
  for (const id of ids) {
    removed.add(key(sessionId, id));
    synced.delete(key(sessionId, id));
  }
  live[sessionId] = (live[sessionId] ?? []).filter((e) => !ids.includes(e.id));
  writeSession(sessionId, listFailedSends(sessionId));
  notify(sessionId);
}

export function removeFailedSend(sessionId: string, id: string) {
  forget(sessionId, [id]);
}

export function clearFailedSends(sessionId: string) {
  forget(sessionId, listFailedSends(sessionId).map((e) => e.id));
}

function parseStore(raw: string | null): Store {
  try {
    const parsed = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

/** Re-publishes sessions another tab changed (native `storage` event). */
export function handleFailedSendsStorageEvent(event: StorageEvent | Event) {
  const { key: changed, oldValue, newValue } = event as StorageEvent;
  if (changed !== STORAGE_KEY && changed !== null) return;
  const sessions = new Set([...Object.keys(parseStore(oldValue)), ...Object.keys(parseStore(newValue)), ...Object.keys(live)]);
  for (const sessionId of sessions) notify(sessionId);
}

if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('storage', handleFailedSendsStorageEvent);
}
