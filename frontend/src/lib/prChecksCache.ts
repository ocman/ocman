import type { CIState, PRChecks } from './upstreamApi';

// Settled CI results keyed by `host/repo@sha`, so a commit's status is fetched
// once per repository; unsettled results are never stored (callers poll). A
// rerun on the same SHA is picked up by the refresh button, which clears it.
const STORAGE_KEY = 'ocman.prChecks.v2';
const MAX_ENTRIES = 1000;

/** How often a visible row re-asks for a non-final (pending/unknown) status. */
export const CI_POLL_MS = 15_000;

const isFinalCIState = (state: CIState) => state === 'success' || state === 'failure';

/**
 * True once every check has finished. The rolled-up state alone is not
 * enough: it reports failure while other checks are still running.
 */
export const isSettled = (checks: PRChecks) =>
  checks.checks.length > 0 && isFinalCIState(checks.state) && checks.checks.every((c) => isFinalCIState(c.state));

export const prChecksCacheKey = (host: string, repo: string, sha: string) => `${host}/${repo}@${sha}`;

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

export function getCachedPRChecks(key: string): PRChecks | undefined {
  return load().get(key);
}

export function cachePRChecks(key: string, checks: PRChecks) {
  if (!isSettled(checks)) return;
  const map = load();
  map.delete(key);
  map.set(key, { state: checks.state, checks: checks.checks });
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
