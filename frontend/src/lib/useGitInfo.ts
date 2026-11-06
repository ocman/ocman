import { useEffect } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchJSON } from './api';
import type { GitInfo } from './api';
import { acquireActivityScope } from './activityScopes';
import { checkoutKey } from './projectIdentity';

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

function gitInfoQuery(dirs: string[] | undefined, remoteId: string, enabled: boolean) {
  const queryParam = buildDirsQueryParam(dirs);
  return {
    queryKey: ['git-info', remoteId, queryParam],
    queryFn: ({ signal }: { signal: AbortSignal }) => fetchGitInfoOnce(queryParam === null ? [] : decodeURIComponent(queryParam).split(','), remoteId, signal),
    enabled: enabled && queryParam !== null,
    refetchInterval: REFRESH_INTERVAL_MS,
  };
}

function useGitActivityScopes(queryParam: string | null, active: boolean) {
  useEffect(() => {
    if (!active || queryParam === null) return;
    const releaseScopes = decodeURIComponent(queryParam).split(',').map((dir) => acquireActivityScope(`git-status:${dir}`));
    return () => releaseScopes.forEach((release) => release());
  }, [active, queryParam]);
}

/** Fetch every sidebar owner's checkouts, independently of the open session. */
export function useCheckoutGitInfos(checkouts: Array<{ directory: string; remoteId?: string }>, enabled = true): Record<string, GitInfo> {
  const client = useQueryClient();
  const owners = new Map<string, string[]>();
  for (const { directory, remoteId } of checkouts) {
    if (!directory) continue;
    const owner = remoteId || 'local';
    const dirs = owners.get(owner) ?? [];
    dirs.push(directory);
    owners.set(owner, dirs);
  }
  const entries = [...owners.entries()].sort(([a], [b]) => a.localeCompare(b));
  const infos = useQueries({
    queries: entries.map(([owner, dirs]) => ({
      ...gitInfoQuery(dirs, owner, enabled),
      // A changed sidebar directory set is a new query. Reuse only this
      // owner's still-requested checkouts until that query has fresh data.
      placeholderData: () => {
        const requested = new Set(dirs.map((dir) => dir.trim()));
        const cached = client.getQueryCache().findAll({ queryKey: ['git-info', owner] })
          .sort((a, b) => a.state.dataUpdatedAt - b.state.dataUpdatedAt);
        return Object.fromEntries(cached.flatMap((query) =>
          Object.entries((query.state.data as Record<string, GitInfo> | undefined) ?? EMPTY)
            .filter(([directory]) => requested.has(directory))));
      },
    })),
    combine: (results) => Object.fromEntries(results.flatMap((result, index) =>
      Object.entries(result.data ?? EMPTY).map(([directory, info]) => [checkoutKey(directory, entries[index][0]), info]))),
  });
  useGitActivityScopes(buildDirsQueryParam(checkouts.map(({ directory }) => directory)), enabled);
  return infos;
}

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
    ...gitInfoQuery(dirs, remoteId, enabled),
    placeholderData: (prev, prevQuery) => (!enabled && prevQuery?.queryKey[1] === remoteId ? prev : undefined),
  });

  useGitActivityScopes(queryParam, active);

  if (queryParam === null) return { infos: EMPTY, loading: false, error: null };
  const error = query.error ? (query.error instanceof Error ? query.error.message : 'Failed to load git info') : null;
  const infos = query.data ?? EMPTY;
  if (!enabled) {
    const requested = new Set(dirList);
    return { infos: Object.fromEntries(Object.entries(infos).filter(([dir]) => requested.has(dir))), loading: false, error };
  }
  return { infos, loading: query.isFetching, error };
}
