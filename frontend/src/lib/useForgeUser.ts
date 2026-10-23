import { useQuery } from '@tanstack/react-query';
import { fetchForgeUser } from './upstreamApi';

/**
 * useForgeUser resolves the authenticated user's login for the given
 * (dir, remote). Returns null when:
 *   - dir/remote are unset, or
 *   - the forge has no credential (so the backend returned 401).
 *
 * Consumers use the login to populate the "mine" filter on the list
 * endpoints. A null result hides / disables the mine toggle for the
 * affected remote.
 */
export function useForgeUser(
  dir: string | undefined,
  remote: string | undefined,
  remoteId: string,
): { login: string | null; loading: boolean; ready: boolean } {
  const enabled = !!dir && !!remote;
  const query = useQuery({
    queryKey: ['forge-user', remoteId, dir, remote],
    queryFn: async ({ signal }) => (await fetchForgeUser({ dir: dir!, remoteId, remote: remote!, signal }))?.login ?? null,
    enabled,
    // A known login is reused for a minute; "no login" is rechecked on mount.
    staleTime: (q) => (q.state.data ? 60_000 : 0),
  });

  // Errors (e.g. 401 unauthenticated) resolve to null, the "no login"
  // signal callers want.
  return {
    login: enabled ? query.data ?? null : null,
    loading: enabled && (query.isPending || query.isFetching),
    ready: enabled && !query.isPending,
  };
}
