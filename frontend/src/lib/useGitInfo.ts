import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
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
const EMPTY: Record<string, GitInfo> = {};

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

/**
 * useGitInfo polls git status for `dirs` every 30 s while mounted and
 * `enabled`. The query key uses the canonical query-param string, so
 * callers can pass a fresh array literal on every render. Polling pauses
 * while the tab is hidden and refreshes on return. When disabled, the
 * last same-owner result stays visible, limited to the requested dirs.
 */
export function useGitInfo(dirs: string[] | undefined, remoteId: string, enabled = true): UseGitInfoResult {
  const queryParam = buildDirsQueryParam(dirs);
  const dirList = queryParam === null ? [] : decodeURIComponent(queryParam).split(',');
  const active = enabled && queryParam !== null;

  const query = useQuery({
    queryKey: ['git-info', remoteId, queryParam],
    queryFn: ({ signal }) => fetchGitInfoOnce(dirList, remoteId, signal),
    enabled: active,
    refetchInterval: REFRESH_INTERVAL_MS,
    placeholderData: (prev, prevQuery) => (!enabled && prevQuery?.queryKey[1] === remoteId ? prev : undefined),
  });

  useEffect(() => {
    if (!active || queryParam === null) return;
    const releaseScopes = decodeURIComponent(queryParam).split(',').map((dir) => acquireActivityScope(`git-status:${dir}`));
    return () => releaseScopes.forEach((release) => release());
  }, [active, queryParam]);

  if (queryParam === null) return { infos: EMPTY, loading: false, error: null };
  const error = query.error ? (query.error instanceof Error ? query.error.message : 'Failed to load git info') : null;
  const infos = query.data ?? EMPTY;
  if (!enabled) {
    const requested = new Set(dirList);
    return { infos: Object.fromEntries(Object.entries(infos).filter(([dir]) => requested.has(dir))), loading: false, error };
  }
  return { infos, loading: query.isFetching, error };
}
