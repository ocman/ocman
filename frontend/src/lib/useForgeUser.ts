import { fetchForgeUser } from './upstreamApi';
import { useAsyncResource } from './useAsyncResource';

const forgeUsers = new Map<string, string>();
const maxForgeUsers = 32;

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
  remoteId: string,
): { login: string | null; loading: boolean; ready: boolean } {
  const key = `${remoteId}\0${dir}\0${remote}`;
  // Errors (e.g. 401 unauthenticated) resolve to null — the resource's
  // initial value — which is exactly the "no login" signal callers want.
  const { data, loading, ready } = useAsyncResource<string | null>({
    fetcher: (signal) => {
      const cached = forgeUsers.get(key);
      if (cached) return Promise.resolve(cached);
      return fetchForgeUser({ dir: dir!, remoteId, remote: remote!, signal }).then((u) => {
        const login = u?.login ?? null;
        if (login !== null) {
          if (forgeUsers.size >= maxForgeUsers) {
            forgeUsers.delete(forgeUsers.keys().next().value!);
          }
          forgeUsers.set(key, login);
        }
        return login;
      });
    },
    deps: [dir, remote, remoteId],
    initial: null,
    enabled: !!dir && !!remote,
  });

  return { login: data, loading, ready };
}
