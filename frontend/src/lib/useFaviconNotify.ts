import { useEffect, useRef } from 'react';
import { api } from './api';

const FAVICON_DEFAULT = '/favicon.svg';
const FAVICON_NOTIFY = '/favicon-notify.svg';
const POLL_INTERVAL_MS = 10_000;

// Module-level recheck trigger set by the active hook instance.
let _recheck: (() => void) | null = null;

/**
 * Triggers a recheck of pending sessions and clears the favicon/title
 * notification if there are none remaining.  Call this after marking a
 * session as seen so the notification clears immediately even when the tab
 * is already visible.
 */
export function recheckFaviconNotify() {
  _recheck?.();
}

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
  // Snapshot of session statuses taken when the tab goes hidden.
  // Only sessions that transition to waiting/error *after* this snapshot count.
  const baselineRef = useRef<Map<string, string> | null>(null);

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
      document.title = `(${count}) ${document.title.replace(/^\(\d+\) /, '')}`;
    }

    function clearNotify() {
      if (!notifyingRef.current) return;
      notifyingRef.current = false;
      setFavicon(FAVICON_DEFAULT);
      document.title = document.title.replace(/^\(\d+\) /, '');
    }

    async function checkPending() {
      try {
        const sessions = await api.sessions();
        const baseline = baselineRef.current;
        const count = sessions.filter(s => {
          if (s.status !== 'waiting' && s.status !== 'error') return false;
          if (s.seen) return false;
          // Only count sessions that were not already in a terminal state
          // when the tab went hidden (i.e. newly completed while away).
          if (baseline !== null && baseline.get(s.id) === s.status) return false;
          return true;
        }).length;
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

    async function takeBaseline() {
      try {
        const sessions = await api.sessions();
        baselineRef.current = new Map(sessions.map(s => [s.id, s.status]));
      } catch {
        baselineRef.current = null;
      }
    }

    _recheck = () => void checkPending();

    function onVisibilityChange() {
      if (!document.hidden) {
        // Tab became visible — clear the notification badge and baseline.
        clearNotify();
        baselineRef.current = null;
        // Stop the poll while visible; the Dashboard / SessionDetail have
        // their own polling, so we avoid redundant requests.
        if (intervalRef.current !== null) {
          clearInterval(intervalRef.current);
          intervalRef.current = null;
        }
      } else {
        // Tab became hidden — snapshot current statuses, then start polling.
        void takeBaseline().then(() => void checkPending());
        if (intervalRef.current === null) {
          intervalRef.current = setInterval(() => void checkPending(), POLL_INTERVAL_MS);
        }
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // Kick off if the page starts hidden (e.g. opened in a background tab).
    if (document.hidden) {
      void takeBaseline().then(() => void checkPending());
      intervalRef.current = setInterval(() => void checkPending(), POLL_INTERVAL_MS);
    }

    return () => {
      _recheck = null;
      document.removeEventListener('visibilitychange', onVisibilityChange);
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      clearNotify();
    };
  }, []);
}
