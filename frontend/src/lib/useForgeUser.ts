import { useEffect, useState } from 'react';
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
export function useForgeUser(dir: string | undefined, remote: string | undefined): string | null {
  const [login, setLogin] = useState<string | null>(null);

  useEffect(() => {
    const run = () => {
      if (!dir || !remote) {
        setLogin(null);
        return undefined;
      }
      const ctrl = new AbortController();
      fetchForgeUser({ dir, remote, signal: ctrl.signal })
        .then((u) => {
          if (ctrl.signal.aborted) return;
          setLogin(u?.login ?? null);
        })
        .catch(() => {
          if (ctrl.signal.aborted) return;
          setLogin(null);
        });
      return ctrl;
    };
    const ctrl = run();
    return () => ctrl?.abort();
  }, [dir, remote]);

  return login;
}
