import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { useNotifyStore, recheckNotifyData } from './useNotifyData';

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

/**
 * Triggers a recheck of pending sessions and clears the favicon/title
 * notification if there are none remaining.  Call this after marking a
 * session as seen, or after responding to a permission/question, so the
 * notification clears immediately even when the tab is already visible.
 */
export function recheckFaviconNotify() {
  recheckNotifyData();
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
 *
 * Now consumes the shared `useNotifyStore` instead of polling
 * `/api/sessions/notify` independently (P2 fix).
 */
export function useFaviconNotify() {
  const currentVariantRef = useRef<VariantName>('default');
  // Snapshot of session statuses taken when the tab goes hidden.
  // Only sessions that transition to waiting/error *after* this snapshot
  // count toward the notify badge.
  const baselineRef = useRef<Map<string, string> | null>(null);

  useEffect(() => {
    // Subscribe to the shared notify store.
    useNotifyStore.getState().subscribe();

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

    function checkPending(sessions: NotifyEntry[]) {
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
    }

    function onVisibilityChange() {
      const sessions = useNotifyStore.getState().data;
      if (!document.hidden) {
        // Tab became visible — reset the notify baseline so the next
        // hide-then-complete cycle starts fresh.  The prompt state may
        // still apply even while visible, so checkPending() decides
        // whether to clear or keep the favicon.
        baselineRef.current = null;
        if (sessions) checkPending(sessions);
      } else {
        // Tab became hidden — snapshot current statuses, then recheck.
        if (sessions) {
          baselineRef.current = new Map(sessions.map(s => [s.id, s.status]));
          checkPending(sessions);
        }
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // Subscribe to store changes to react when new data arrives.
    const unsub = useNotifyStore.subscribe((state) => {
      if (state.data) checkPending(state.data);
    });

    // Initial check with current data.
    const initial = useNotifyStore.getState().data;
    if (initial) checkPending(initial);

    return () => {
      unsub();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      useNotifyStore.getState().unsubscribe();
      applyState('default', null);
    };
  }, []);
}
