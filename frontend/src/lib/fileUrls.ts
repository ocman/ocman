import { useEffect, useState } from 'react';
import { postJSON } from './api.requests';

// Embedded-file URLs are /api/file/<path>.<unix expiry>.<mac>. Transcripts keep
// them forever, but the server serves each for about a week, so the dashboard
// re-mints stale ones when it renders them.
const FILE_URL = /^(?:\/api\/remote-resources\/[^/]+)?\/api\/file\/([^/?#]+)$/;
const REFRESH_MARGIN_MS = 24 * 60 * 60 * 1000;
// Per stale URL: the in-flight or settled refresh, and its replacement once
// known so the entry can be evicted when that replacement nears expiry too.
const refreshed = new Map<string, { pending: Promise<string>; url?: string }>();

/** Whether an origin-relative embedded-file URL must be re-minted before use:
 * tokens from before URLs expired, and ones expiring within a day. */
export function fileURLNeedsRefresh(href: string, now = Date.now()): boolean {
  const token = FILE_URL.exec(href)?.[1];
  if (!token) return false;
  const parts = token.split('.');
  if (parts.length === 2) return true;
  if (parts.length !== 3 || !/^\d+$/.test(parts[1])) return false;
  return Number(parts[1]) * 1000 - now < REFRESH_MARGIN_MS;
}

/** Asks the server for a fresh URL for the same file. Concurrent callers share
 * one request, and later ones reuse its result until that URL needs a refresh
 * itself, so a long-lived dashboard never hands out an expired replacement. */
export function refreshFileURL(href: string): Promise<string> {
  const cached = refreshed.get(href);
  if (cached && !(cached.url && fileURLNeedsRefresh(cached.url))) return cached.pending;
  const prefix = href.match(/^\/api\/remote-resources\/[^/]+/)?.[0] ?? '';
  const entry: { pending: Promise<string>; url?: string } = {
    pending: postJSON<{ url: string }>(`${prefix}/api/files/refresh`, { url: href.slice(prefix.length) }).then(resp => {
      entry.url = prefix + resp.url;
      return entry.url;
    }),
  };
  refreshed.set(href, entry);
  entry.pending.catch(() => {
    if (refreshed.get(href) === entry) refreshed.delete(href);
  });
  return entry.pending;
}

/** Returns href, swapped for a re-minted URL once a stale one is refreshed.
 * A failed refresh keeps the original, which the server then rejects. */
export function useFreshFileURL(href: string | undefined): string | undefined {
  const [fresh, setFresh] = useState<{ from: string; url: string }>();
  useEffect(() => {
    if (!href || !fileURLNeedsRefresh(href)) return;
    let live = true;
    refreshFileURL(href).then(url => { if (live) setFresh({ from: href, url }); }, () => {});
    return () => { live = false; };
  }, [href]);
  return fresh && fresh.from === href ? fresh.url : href;
}

/** Test hook: forget refreshed URLs between cases. */
export function resetFileURLRefreshes() {
  refreshed.clear();
}
