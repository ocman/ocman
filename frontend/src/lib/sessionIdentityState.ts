import { getDraft, migrateDraft } from './composerDraft';
import { listFailedSends, recordFailedSend } from './failedSends';
import type { MessageBookmark } from './messageBookmarks';
import type { SessionIdentity } from './sessionIdentity';
import { lookupSessionIdentity } from './sessionIdentity';
import { transact } from './draftDb';
import type { DraftStart } from './draftStartClaims';
import { SESSION_IDENTITY_RECEIPT_PREFIX } from './sessionIdentityReceipts';

const adopted = new Set<string>();
const inFlight = new Map<string, Promise<void>>();
const ensured = new Set<string>();
const ensuring = new Map<string, Promise<void>>();
const listeners = new Set<(id: string) => void>();
const bookmarkKey = (id: string) => `ocman:message-bookmarks:${id}`;
const receiptKey = (identity: SessionIdentity) => `${SESSION_IDENTITY_RECEIPT_PREFIX}${identity.ownerId}:${identity.id}`;

export function onSessionBrowserStateAdopted(listener: (id: string) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

// First-delivery reconciliation awaits this before declaring a session ready.
// A canonical deep link must not bypass a legacy uncertain-delivery receipt.
export function ensureSessionBrowserState(id: string): Promise<void> {
  if (!id.startsWith('ocm_') || ensured.has(id)) return Promise.resolve();
  const current = ensuring.get(id);
  if (current) return current;
  const promise = lookupSessionIdentity(id).then(migrateSessionBrowserState).then(() => { ensured.add(id); }).finally(() => { ensuring.delete(id); });
  ensuring.set(id, promise);
  return promise;
}

// Unqualified legacy browser data cannot identify a remote owner when two
// machines have the same native ID. Import it only for the local platform.
// Owner-qualified entries can be imported on any machine. Keep source copies
// so a failed storage write or an old tab cannot destroy user recovery data.
export function migrateSessionBrowserState(identity: SessionIdentity): Promise<void> {
  const receipt = receiptKey(identity);
  if (adopted.has(receipt)) return Promise.resolve();
  const current = inFlight.get(receipt);
  if (current) return current;
  const promise = migrate(identity, receipt).finally(() => { inFlight.delete(receipt); });
  inFlight.set(receipt, promise);
  return promise;
}

async function migrate(identity: SessionIdentity, receipt: string) {
  const storage = window.localStorage;
  if (storage.getItem(receipt)) { adopted.add(receipt); return; }
  const claim = `ocman:legacy-session-owner:${identity.ownerId}:${identity.platform}:${identity.nativeId}`;
  const claimed = storage.getItem(claim);
  if (claimed && claimed !== identity.id) { adopted.add(receipt); ensured.add(identity.id); return; }
  const from = identity.platform.includes(':') ? `${identity.platform}:${identity.nativeId}` : identity.nativeId;
  const to = identity.id;
  if (from === to) return;

  const readBookmarks = (id: string): MessageBookmark[] => {
    const raw = storage.getItem(bookmarkKey(id));
    if (!raw) return [];
    const value: unknown = JSON.parse(raw);
    if (!Array.isArray(value)) throw new Error('Stored message bookmarks could not be migrated');
    return value.filter((b): b is MessageBookmark => !!b && typeof b === 'object' && typeof b.id === 'string');
  };
  const oldBookmarks = readBookmarks(from);
  if (oldBookmarks.length) {
    const target = readBookmarks(to);
    const ids = new Set(target.map((b) => b.id));
    const merged = [...target, ...oldBookmarks.filter((b) => !ids.has(b.id)).map((b) => ({ ...b, sessionId: to }))];
    storage.setItem(bookmarkKey(to), JSON.stringify(merged));
  }
  const canonicalFailed = new Map(listFailedSends(to).map((entry) => [entry.id, entry]));
  const persistedFailed = JSON.parse(storage.getItem('ocman.failedSends.v1') || '{}') as Record<string, { id: string }[]>;
  const failedIDs = new Set((persistedFailed[to] || []).map((entry) => entry.id));
  const failed = listFailedSends(from);
  for (const entry of failed) {
    if (!failedIDs.has(entry.id)) recordFailedSend(to, canonicalFailed.get(entry.id) ?? entry);
  }
  if (failed.length) {
    const stored = JSON.parse(storage.getItem('ocman.failedSends.v1') || '{}') as Record<string, { id: string }[]>;
    if (!failed.every((entry) => stored[to]?.some((saved) => saved.id === entry.id))) throw new Error('Failed-send recovery could not be migrated');
  }
  // Reuse the existing atomic text relocation. Never discard a different text
  // already stored under the canonical ID, or replay a first-delivery receipt.
  if (getDraft(from) && !getDraft(to) && !await migrateDraft(from, to)) {
    throw new Error('The existing composer text could not be migrated');
  }
  await transact(['starts'], 'readwrite', async (tx) => {
    const [source, target] = await Promise.all([
      tx.get<DraftStart>('starts', `first-delivery:${from}`), tx.get<DraftStart>('starts', `first-delivery:${to}`),
    ]);
    if (source && !target) tx.put('starts', `first-delivery:${to}`, source);
  });
  storage.setItem(claim, to);
  storage.setItem(receipt, JSON.stringify({ from, to }));
  adopted.add(receipt);
  ensured.add(to);
  for (const listener of listeners) listener(to);
}

export function resetSessionIdentityStateForTests() { adopted.clear(); inFlight.clear(); ensured.clear(); ensuring.clear(); }
