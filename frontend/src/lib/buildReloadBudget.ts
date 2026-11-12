const KEY = 'ocman:preload-recovery:v1';
export const BUILD_HASH = /^[a-f0-9]{64}$/;
type Attempt = { from: string; to: string; at: number };

/** One tab-local budget shared by lazy-chunk recovery and the update countdown. */
export function claimBuildReload(from: string, to: string) {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(KEY) || '[]');
    if (!Array.isArray(value) || value.length > 8 || !value.every((item): item is Attempt =>
      item && BUILD_HASH.test(item.from) && BUILD_HASH.test(item.to) && Number.isFinite(item.at))) return false;
    const now = Date.now();
    // Cache-stuck HTML and deployment oscillation cannot repeatedly reload the
    // same builds. Also cap rapid, distinct deployments at two reloads/minute.
    if (value.some(item => item.from === from || item.to === to) ||
        value.filter(item => now - item.at < 60_000).length >= 2) return false;
    sessionStorage.setItem(KEY, JSON.stringify([...value, { from, to, at: now }].slice(-8)));
    return true;
  } catch { return false; } // No durable guard means no safe automatic reload.
}
