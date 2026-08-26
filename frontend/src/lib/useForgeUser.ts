import { fetchForgeUser } from './upstreamApi';
import { useAsyncResource } from './useAsyncResource';

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
  remoteId = 'local',
): string | null {
  // Errors (e.g. 401 unauthenticated) resolve to null — the resource's
  // initial value — which is exactly the "no login" signal callers want.
  const { data } = useAsyncResource<string | null>({
    fetcher: (signal) =>
      fetchForgeUser({ dir: dir!, remoteId, remote: remote!, signal }).then((u) => u?.login ?? null),
    deps: [dir, remote, remoteId],
    initial: null,
    enabled: !!dir && !!remote,
  });

  return data;
}
