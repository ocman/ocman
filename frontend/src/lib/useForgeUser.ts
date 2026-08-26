import { fetchForgeUser } from './upstreamApi';
import { useAsyncResource } from './useAsyncResource';

const forgeUsers = new Map<string, Promise<string | null>>();

export function _resetForgeUserCacheForTests(): void {
  forgeUsers.clear();
}

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
  const key = `${remoteId}\0${dir}\0${remote}`;
  // Errors (e.g. 401 unauthenticated) resolve to null — the resource's
  // initial value — which is exactly the "no login" signal callers want.
  const { data } = useAsyncResource<string | null>({
    fetcher: () => {
      let request = forgeUsers.get(key);
      if (!request) {
        request = fetchForgeUser({ dir: dir!, remoteId, remote: remote! })
          .then((u) => {
            const login = u?.login ?? null;
            if (login === null) forgeUsers.delete(key);
            return login;
          })
          .catch((err) => {
            forgeUsers.delete(key);
            throw err;
          });
        forgeUsers.set(key, request);
      }
      return request;
    },
    deps: [dir, remote, remoteId],
    initial: null,
    enabled: !!dir && !!remote,
  });

  return data;
}
