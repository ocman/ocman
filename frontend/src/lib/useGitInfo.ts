import { useEffect, useRef, useState } from 'react';
import { fetchJSON } from './api';
import type { GitInfo } from './api';
import { acquireActivityScope } from './activityScopes';

/**
 * useGitInfo subscribes to git-status info for a set of directories
 * and refreshes it on a slow cadence while the consuming component
 * is mounted. It is the replacement for the per-/api/sessions
 * fork-fan-out pattern that used to populate session.gitInfo
 * eagerly: components that want a branch indicator now opt in by
 * mounting this hook with the directories they care about, and the
 * subprocess work is scoped to "the user is actually looking at
 * something for which git info is interesting".
 *
 * The hook is deliberately non-clever:
 *   - polling, not push (no SSE);
 *   - one fetch coalesces all dirs in the input via a comma-separated
 *     `dirs` query param;
 *   - paused while `document.hidden`;
 *   - skipped entirely when the input is empty (no fetch fires).
 *
 * Refresh cadence is tuned to feel "live enough" for a branch label
 * (which only changes on git checkout / commit) without flooding
 * the server. 30 s mirrors the gitinfo cache TTL on the backend, so
 * each refresh roughly hits the cache once it's warm.
 */

const REFRESH_INTERVAL_MS = 30_000;

export interface UseGitInfoResult {
  infos: Record<string, GitInfo>;
  loading: boolean;
  error: string | null;
}

/**
 * buildDirsQueryParam normalises the `dirs` input the same way the
 * hook does: drop empties, dedup, sort. The sort is critical so the
 * hook's effect dependency array is stable across re-renders that
 * happen to reorder the input — without it, the hook would refetch
 * on every parent re-render that reshuffles the session list.
 *
 * Returns null when there's nothing useful to fetch so callers can
 * short-circuit before touching the network.
 *
 * Exported for unit testing; not part of the hook's public surface.
 */
export function buildDirsQueryParam(dirs: string[] | undefined): string | null {
  if (!dirs || dirs.length === 0) return null;
  const seen = new Set<string>();
  for (const d of dirs) {
    const trimmed = (d ?? '').trim();
    if (trimmed) seen.add(trimmed);
  }
  if (seen.size === 0) return null;
  const sorted = Array.from(seen).sort();
  return encodeURIComponent(sorted.join(','));
}

/**
 * fetchGitInfoOnce performs a single GET to /api/git/info and
 * returns the parsed map. Aborts (component unmount) collapse to
 * an empty map rather than throwing — the caller has already moved
 * on, so there's nothing to report.
 *
 * Exported so the hook's network behaviour is testable without
 * React state plumbing.
 */
export async function fetchGitInfoOnce(
  dirs: string[],
  remoteId = 'local',
  signal?: AbortSignal,
): Promise<Record<string, GitInfo>> {
  const param = buildDirsQueryParam(dirs);
  if (param === null) return {};

  // fetchJSON (not raw fetch) so a 401 raises AuthError and reaches the
  // lockscreen hook instead of surfacing as a generic pane error.
  try {
    return await fetchJSON<Record<string, GitInfo>>(`/api/git/info?dirs=${param}&remoteId=${encodeURIComponent(remoteId)}`, signal);
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') return {};
    if (signal?.aborted) return {};
    throw err;
  }
}

// Module-level helpers exposed for tests; not part of the public API.
// Currently a no-op but kept for symmetry with the other hooks in
// this directory (useLongTaskMonitor etc.) so a future refactor can
// add a shared cache without changing the test surface.
export function _resetForTests(): void { /* intentionally empty */ }

/**
 * useGitInfo is the React hook side. The effect depends on the
 * canonical query-param *string* rather than the array reference,
 * so callers can pass a fresh array literal on every render
 * (`useGitInfo(sessions.map(s => s.dir))`) without making the
 * effect re-run and aborting in-flight fetches.
 *
 * That subtlety bit us once: an earlier version memoised the dirs
 * array via useMemo([...], [dirs]), which still keyed on the array
 * reference and re-ran on every parent render. Each render aborted
 * the previous fetch before it could land, so /api/git/info was
 * never observed completing. Keying on the string fixes it because
 * `buildDirsQueryParam` is deterministic on the input contents.
 */
export function useGitInfo(dirs: string[] | undefined, remoteId: string): UseGitInfoResult {
  // Compute the canonical query param fresh on every render. It's
  // O(n) in the dir count, which is small (a sidebar's worth), so
  // skipping useMemo here is fine and avoids the array-identity
  // trap.
  const queryParam = buildDirsQueryParam(dirs);
  const requestKey = `${remoteId}\0${queryParam ?? ''}`;

  const [infos, setInfos] = useState<Record<string, GitInfo>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeKey, setActiveKey] = useState(requestKey);

  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    // No dirs to track — stand down. State (infos/loading/error)
    // intentionally retains whatever it last held; consumers that
    // pass an empty dirs array are signalling "I no longer care",
    // so the stale value won't be observed. Resetting here would
    // trigger a setState during the post-render commit phase,
    // which the React Compiler flags as a cascading-renders
    // antipattern.
    if (queryParam === null) return;

    const dirList = decodeURIComponent(queryParam).split(',');
    const releaseScopes = dirList.map((dir) => acquireActivityScope(`git-status:${dir}`));
    const reset = () => {
      setActiveKey(requestKey);
      setInfos({});
      setError(null);
    };
    reset();

    const runFetch = () => {
      if (typeof document !== 'undefined' && document.hidden) {
        // Tab is in the background; skip this tick and let the
        // visibilitychange listener trigger an immediate refresh
        // when the user returns.
        return;
      }
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setLoading(true);
      fetchGitInfoOnce(dirList, remoteId, controller.signal)
        .then((res) => {
          if (controller.signal.aborted) return;
          setInfos(res);
          setError(null);
          setLoading(false);
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof Error && err.name === 'AbortError') return;
          setError(err instanceof Error ? err.message : 'Failed to load git info');
          setLoading(false);
        });
    };

    // Initial fetch on mount / when the dirs change.
    runFetch();
    const id = setInterval(runFetch, REFRESH_INTERVAL_MS);

    // visibilitychange: refresh immediately on tab return so the
    // user sees fresh data without waiting up to REFRESH_INTERVAL_MS.
    const onVisibility = () => {
      if (typeof document !== 'undefined' && !document.hidden) runFetch();
    };
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', onVisibility);
    }

    return () => {
      releaseScopes.forEach((release) => release());
      clearInterval(id);
      abortRef.current?.abort();
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', onVisibility);
      }
    };
  }, [queryParam, remoteId, requestKey]);

  if (queryParam === null) return { infos: {}, loading: false, error: null };
  if (activeKey !== requestKey) return { infos: {}, loading: true, error: null };
  return { infos, loading, error };
}
