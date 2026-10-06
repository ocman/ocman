import type { CIState, PRChecks } from './upstreamApi';

// Final CI results keyed by head SHA. A SHA's final status never changes,
// so it is fetched once; non-final states are never stored (callers poll).
const STORAGE_KEY = 'ocman.prChecks.v1';
const MAX_ENTRIES = 1000;

/** How often a visible row re-asks for a non-final (pending/unknown) status. */
export const CI_POLL_MS = 15_000;

export const isFinalCIState = (state: CIState) => state === 'success' || state === 'failure';

let entries: Map<string, PRChecks> | null = null;

function load(): Map<string, PRChecks> {
  if (entries) return entries;
  try {
    entries = new Map(JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? '[]'));
  } catch {
    entries = new Map();
  }
  return entries;
}

function save(map: Map<string, PRChecks>) {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...map]));
  } catch {
    // Quota or disabled storage: the in-memory copy still serves this load.
  }
}

export function getCachedPRChecks(sha: string): PRChecks | undefined {
  return load().get(sha);
}

export function cachePRChecks(sha: string, checks: PRChecks) {
  if (!isFinalCIState(checks.state)) return;
  const map = load();
  map.delete(sha);
  map.set(sha, { state: checks.state, checks: checks.checks });
  // ponytail: insertion-order eviction (oldest written first), not LRU on reads.
  for (const key of map.keys()) {
    if (map.size <= MAX_ENTRIES) break;
    map.delete(key);
  }
  save(map);
}

export function clearPRChecksCache() {
  entries = new Map();
  try {
    window.localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
}

/** Drops the in-memory copy so the next read reloads localStorage (a page reload). */
export function resetPRChecksMemoryForTest() {
  entries = null;
}
