// Persistent storage of message sends that failed on the client.
//
// When `sendMessage` rejects (network down, 5xx, etc.) we lose the user's
// prompt. Persisting the failed send lets us:
//   1. Show a Retry button on the user's message bubble so they don't have to
//      retype the prompt.
//   2. Survive a page refresh — the prompt stays retryable until the user
//      explicitly retries or dismisses it.
//
// State is kept in localStorage under a single key, scoped per session id.
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

function loadStore(): Store {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return parsed as Store;
  } catch {
    return {};
  }
}

function saveStore(data: Store) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
  } catch {
    // Quota exceeded / private mode / disabled storage. Nothing we can do
    // here; the in-memory state in SessionDetail still reflects the failure
    // for the current page lifetime.
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

export function listFailedSends(sessionId: string): FailedSend[] {
  const store = loadStore();
  const list = store[sessionId];
  return Array.isArray(list) ? list : [];
}

export function recordFailedSend(sessionId: string, entry: FailedSend) {
  const store = loadStore();
  const fitted = fitEntry(entry);
  const existing = Array.isArray(store[sessionId]) ? store[sessionId] : [];
  const idx = existing.findIndex((e) => e.id === fitted.id);
  const next = idx >= 0
    ? existing.map((e, i) => (i === idx ? fitted : e))
    : [...existing, fitted];
  store[sessionId] = next;
  saveStore(store);
}

export function removeFailedSend(sessionId: string, id: string) {
  const store = loadStore();
  const existing = store[sessionId];
  if (!Array.isArray(existing)) return;
  const next = existing.filter((e) => e.id !== id);
  if (next.length === 0) {
    delete store[sessionId];
  } else {
    store[sessionId] = next;
  }
  saveStore(store);
}

export function clearFailedSends(sessionId: string) {
  const store = loadStore();
  if (!(sessionId in store)) return;
  delete store[sessionId];
  saveStore(store);
}
