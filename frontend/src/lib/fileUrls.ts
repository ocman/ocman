import { useEffect, useState } from 'react';
import { postJSON } from './api.requests';

// Embedded-file URLs are /api/file/<path>.<unix expiry>.<mac>. Transcripts keep
// them forever, but the server serves each for about a week, so the dashboard
// re-mints stale ones when it renders them.
const FILE_URL = /^\/api\/file\/([^/?#]+)$/;
const REFRESH_MARGIN_MS = 24 * 60 * 60 * 1000;
const refreshed = new Map<string, Promise<string>>();

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

/** Asks the server for a fresh URL for the same file; shared per page load. */
export function refreshFileURL(href: string): Promise<string> {
  let pending = refreshed.get(href);
  if (!pending) {
    pending = postJSON<{ url: string }>('/api/files/refresh', { url: href }).then(resp => resp.url);
    refreshed.set(href, pending);
    pending.catch(() => refreshed.delete(href));
  }
  return pending;
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
