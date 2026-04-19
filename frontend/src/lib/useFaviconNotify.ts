import { useEffect, useRef } from 'react';
import { api } from './api';

// Favicon variants.  We swap all three link tags so the notification state is
// visible everywhere: SVG for modern desktop browsers, PNG as a generic
// fallback, and the apple-touch-icon for iOS/iPadOS Safari (which ignores SVG
// favicons entirely).
const FAVICON_VARIANTS = {
  default: {
    svg: '/favicon.svg',
    png: '/favicon-32.png',
    apple: '/apple-touch-icon.png',
  },
  notify: {
    svg: '/favicon-notify.svg',
    png: '/favicon-notify-32.png',
    apple: '/apple-touch-icon-notify.png',
  },
  prompt: {
    svg: '/favicon-prompt.svg',
    png: '/favicon-prompt-32.png',
    apple: '/apple-touch-icon-prompt.png',
  },
} as const;
type VariantName = keyof typeof FAVICON_VARIANTS;

const POLL_INTERVAL_MS = 10_000;
// Lookback window for /api/sessions/notify. Any reasonable upper bound
// works — the endpoint already filters server-side to only sessions
// that could drive the notification, so the response stays small even
// at 7 days.
const FAVICON_LOOKBACK_MS = 7 * 24 * 60 * 60 * 1000;
const FAVICON_LIMIT = 500;

// Module-level recheck trigger set by the active hook instance.
let _recheck: (() => void) | null = null;

/**
 * Triggers a recheck of pending sessions and clears the favicon/title
 * notification if there are none remaining.  Call this after marking a
 * session as seen, or after responding to a permission/question, so the
 * notification clears immediately even when the tab is already visible.
 */
export function recheckFaviconNotify() {
  _recheck?.();
}

/**
 * Updates the favicon and document title based on two independent signals:
 *
 * 1. **Prompt state** (highest priority): any session has a pending permission
 *    request or question prompt.  This is a user-blocking situation — the
 *    session cannot proceed until the user responds — so the attention
 *    favicon is shown regardless of tab visibility, and the title is prefixed
 *    with `(!)`.
 *
 * 2. **Notify state**: sessions have finished running (status "waiting" or
 *    "error") but have not been seen yet, AND the tab is not currently
 *    visible.  Clears as soon as the tab becomes visible again.  The title
 *    is prefixed with `(N)` where N is the count.
 *
 * Prompt wins over notify when both are active.  Individual sessions are
 * marked seen separately by the session detail page when they are opened.
 */
export function useFaviconNotify() {
  const currentVariantRef = useRef<VariantName>('default');
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  // Snapshot of session statuses taken when the tab goes hidden.
  // Only sessions that transition to waiting/error *after* this snapshot
  // count toward the notify badge.
  const baselineRef = useRef<Map<string, string> | null>(null);

  useEffect(() => {
    function setLinkHref(selector: string, href: string, create: () => HTMLLinkElement) {
      let link = document.querySelector<HTMLLinkElement>(selector);
      if (!link) {
        link = create();
        document.head.appendChild(link);
      }
      const resolved = new URL(href, document.baseURI).href;
      if (link.href !== resolved) link.href = href;
    }

    function applyVariant(name: VariantName) {
      const variant = FAVICON_VARIANTS[name];
      setLinkHref('link[rel="icon"][type="image/svg+xml"]', variant.svg, () => {
        const l = document.createElement('link');
        l.rel = 'icon';
        l.type = 'image/svg+xml';
        return l;
      });
      setLinkHref('link[rel="icon"][type="image/png"]', variant.png, () => {
        const l = document.createElement('link');
        l.rel = 'icon';
        l.type = 'image/png';
        return l;
      });
      setLinkHref('link[rel="apple-touch-icon"]', variant.apple, () => {
        const l = document.createElement('link');
        l.rel = 'apple-touch-icon';
        return l;
      });
      currentVariantRef.current = name;
    }

    function stripTitlePrefix(title: string): string {
      // Strip either `(!) ` or `(N) ` from the front.
      return title.replace(/^\((?:!|\d+)\) /, '');
    }

    function setTitlePrefix(prefix: string | null) {
      const base = stripTitlePrefix(document.title);
      document.title = prefix === null ? base : `${prefix} ${base}`;
    }

    function applyState(variant: VariantName, titlePrefix: string | null) {
      if (currentVariantRef.current !== variant) applyVariant(variant);
      const expected = titlePrefix === null
        ? stripTitlePrefix(document.title)
        : `${titlePrefix} ${stripTitlePrefix(document.title)}`;
      if (document.title !== expected) setTitlePrefix(titlePrefix);
    }

    async function checkPending() {
      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - FAVICON_LOOKBACK_MS,
          limit: FAVICON_LIMIT,
        });

        // Prompt state: any session currently blocked on user input.  We do
        // not apply the baseline filter here — a pending permission/question
        // is blocking *right now*, regardless of when it started.
        const hasPrompt = sessions.some(s => s.pendingPermission || s.pendingQuestion);

        if (hasPrompt) {
          applyState('prompt', '(!)');
          return;
        }

        // Notify state: sessions that transitioned to a terminal state
        // *after* the tab went hidden and have not been seen. The endpoint
        // already filters out seen sessions and non-terminal statuses, so
        // we only need to apply the baseline filter here.
        const baseline = baselineRef.current;
        const notifyCount = sessions.filter(s => {
          if (baseline !== null && baseline.get(s.id) === s.status) return false;
          return true;
        }).length;

        if (notifyCount > 0 && document.hidden) {
          applyState('notify', `(${notifyCount})`);
        } else {
          applyState('default', null);
        }
      } catch {
        // silently ignore — network errors shouldn't break anything
      }
    }

    async function takeBaseline() {
      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - FAVICON_LOOKBACK_MS,
          limit: FAVICON_LIMIT,
        });
        baselineRef.current = new Map(sessions.map(s => [s.id, s.status]));
      } catch {
        baselineRef.current = null;
      }
    }

    _recheck = () => void checkPending();

    function startPolling() {
      if (intervalRef.current === null) {
        intervalRef.current = setInterval(() => void checkPending(), POLL_INTERVAL_MS);
      }
    }

    function stopPolling() {
      if (intervalRef.current !== null) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    }

    function onVisibilityChange() {
      if (!document.hidden) {
        // Tab became visible — reset the notify baseline so the next
        // hide-then-complete cycle starts fresh.  The prompt state may
        // still apply even while visible, so checkPending() decides
        // whether to clear or keep the favicon.
        baselineRef.current = null;
        void checkPending();
      } else {
        // Tab became hidden — snapshot current statuses, then recheck.
        void takeBaseline().then(() => void checkPending());
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // Poll continuously (visible or hidden) so the prompt favicon appears
    // promptly when a permission/question arrives, even while the user is
    // on a different tab in the same window.  Sessions list is cheap and
    // already polled elsewhere; one extra request per 10s is negligible.
    if (document.hidden) {
      // Hidden on startup — take a baseline so notify counts only NEW
      // completions rather than every already-finished session.
      void takeBaseline().then(() => void checkPending());
    } else {
      void checkPending();
    }
    startPolling();

    return () => {
      _recheck = null;
      document.removeEventListener('visibilitychange', onVisibilityChange);
      stopPolling();
      applyState('default', null);
    };
  }, []);
}
