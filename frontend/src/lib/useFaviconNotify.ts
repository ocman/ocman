import { useEffect, useRef } from 'react';
import { api } from './api';

const FAVICON_DEFAULT = '/favicon.svg';
const FAVICON_NOTIFY = '/favicon-notify.svg';
const POLL_INTERVAL_MS = 10_000;

/**
 * Swaps the favicon and document title prefix when there are sessions that have
 * finished running (status "waiting" or "error") but have not been seen yet,
 * and the browser tab is not currently focused / visible.
 *
 * The notification clears as soon as the tab becomes visible again (the user
 * has had a chance to notice).  Individual sessions are marked seen separately
 * by the session detail page when they are actually opened.
 */
export function useFaviconNotify() {
  const pendingCountRef = useRef(0);
  const notifyingRef = useRef(false);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    function setFavicon(href: string) {
      let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
      if (!link) {
        link = document.createElement('link');
        link.rel = 'icon';
        link.type = 'image/svg+xml';
        document.head.appendChild(link);
      }
      if (link.href !== href) link.href = href;
    }

    function applyNotify(count: number) {
      notifyingRef.current = true;
      setFavicon(FAVICON_NOTIFY);
      if (!document.title.startsWith('(•)')) {
        document.title = `(•) ${document.title}`;
      }
      // keep the count in the title current
      const titleWithoutBadge = document.title.replace(/^\(•\) /, '');
      document.title = `(•) ${titleWithoutBadge}`;
      void count; // count reserved for future use (e.g. "(3)")
    }

    function clearNotify() {
      if (!notifyingRef.current) return;
      notifyingRef.current = false;
      setFavicon(FAVICON_DEFAULT);
      document.title = document.title.replace(/^\(•\) /, '');
    }

    async function checkPending() {
      try {
        const sessions = await api.sessions();
        const count = sessions.filter(
          s => (s.status === 'waiting' || s.status === 'error') && !s.seen,
        ).length;
        pendingCountRef.current = count;
        if (count > 0 && document.hidden) {
          applyNotify(count);
        } else {
          clearNotify();
        }
      } catch {
        // silently ignore — network errors shouldn't break anything
      }
    }

    function onVisibilityChange() {
      if (!document.hidden) {
        // Tab became visible — clear the notification badge immediately.
        clearNotify();
        // Stop the poll while visible; the Dashboard / SessionDetail have
        // their own polling, so we avoid redundant requests.
        if (intervalRef.current !== null) {
          clearInterval(intervalRef.current);
          intervalRef.current = null;
        }
      } else {
        // Tab became hidden — start polling and check immediately.
        void checkPending();
        if (intervalRef.current === null) {
          intervalRef.current = setInterval(() => void checkPending(), POLL_INTERVAL_MS);
        }
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // Kick off if the page starts hidden (e.g. opened in a background tab).
    if (document.hidden) {
      void checkPending();
      intervalRef.current = setInterval(() => void checkPending(), POLL_INTERVAL_MS);
    }

    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange);
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      clearNotify();
    };
  }, []);
}
