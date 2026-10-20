export const SESSION_IDENTITY_RECEIPT_PREFIX = 'ocman:session-identity:';

/** Legacy copies stay recoverable, but must not appear as duplicate bookmarks. */
export function adoptedLegacySessionKeys(storage: Storage): Set<string> {
  const keys = new Set<string>();
  try {
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (!key?.startsWith(SESSION_IDENTITY_RECEIPT_PREFIX)) continue;
      try {
        const receipt: unknown = JSON.parse(storage.getItem(key) || 'null');
        if (receipt && typeof receipt === 'object' && 'from' in receipt && typeof receipt.from === 'string' && 'to' in receipt && typeof receipt.to === 'string' && receipt.to.startsWith('ocm_')) keys.add(receipt.from);
      } catch { /* Ignore an old or malformed receipt, never delete its data. */ }
    }
  } catch { /* Storage can be disabled while the page is open. */ }
  return keys;
}
