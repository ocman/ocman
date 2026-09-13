import { useCallback, useEffect, useState } from 'react';
import { api, type ShareLink } from './api';
import { copyToClipboard } from './clipboard';

/** The subset of a share link every list needs; both link shapes satisfy it. */
export type ShareLinkLike = Pick<ShareLink, 'token' | 'url'>;

/**
 * useShareLinks holds the state shared by every public-link list:
 * loading, busy, error, transient copy feedback, and revocation.
 * `revokeSessionId` picks the session a link belongs to, since global
 * lists carry it on the link while a session modal has one fixed id.
 */
export function useShareLinks<T extends ShareLinkLike>(
  load: () => Promise<T[]>,
  revokeSessionId: (link: T) => string,
) {
  const [links, setLinks] = useState<T[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
    let cancelled = false;
    load().then(
      (list) => {
        if (cancelled) return;
        setLinks(list);
        setLoaded(true);
      },
      (err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load share links');
      },
    );
    return () => {
      cancelled = true;
    };
  }, [load]);

  const flashCopied = useCallback((token: string) => {
    setCopied(token);
    window.setTimeout(() => setCopied((c) => (c === token ? null : c)), 2000);
  }, []);

  const copy = useCallback(async (link: T) => {
    if (await copyToClipboard(link.url)) flashCopied(link.token);
    else setError('Could not copy to clipboard');
  }, [flashCopied]);

  /** Run a mutation under the shared busy/error state. */
  const run = useCallback(async (fn: () => Promise<void>, fallback: string) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback);
    } finally {
      setBusy(false);
    }
  }, []);

  const revoke = useCallback((link: T) =>
    run(async () => {
      await api.revokeShareLink(revokeSessionId(link), link.token);
      setLinks((prev) => prev.filter((l) => l.token !== link.token));
    }, 'Failed to revoke share link'),
  [run, revokeSessionId]);

  return { links, setLinks, setLoaded, loaded, busy, error, setError, copied, copy, revoke, run, flashCopied };
}
