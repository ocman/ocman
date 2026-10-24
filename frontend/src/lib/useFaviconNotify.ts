import { useEffect } from 'react';
import { useNotifyBaseline } from './useNotifyBaseline';

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
}

function stripTitlePrefix(title: string): string {
  // Strip either `(!) ` or `(N) ` from the front.
  return title.replace(/^\((?:!|\d+)\) /, '');
}

function setTitlePrefix(prefix: string | null) {
  const base = stripTitlePrefix(document.title);
  document.title = prefix === null ? base : `${prefix} ${base}`;
}

let currentVariant: VariantName = 'default';

function applyState(variant: VariantName, titlePrefix: string | null) {
  if (currentVariant !== variant) {
    applyVariant(variant);
    currentVariant = variant;
  }
  const expected = titlePrefix === null
    ? stripTitlePrefix(document.title)
    : `${titlePrefix} ${stripTitlePrefix(document.title)}`;
  if (document.title !== expected) setTitlePrefix(titlePrefix);
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
  // Only sessions that transition to waiting/error *after* the baseline
  // (taken when the tab goes hidden) count toward the notify badge. The
  // baseline is cleared when the tab becomes visible, and the prompt state
  // may still apply then, so the check reruns.
  useNotifyBaseline((sessions, baseline) => {
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
    const notifyCount = sessions.filter(s => {
      if (baseline !== null && baseline.get(s.id) === s.status) return false;
      return true;
    }).length;

    if (notifyCount > 0 && document.hidden) {
      applyState('notify', `(${notifyCount})`);
    } else {
      applyState('default', null);
    }
  }, { key: (s) => s.status, initialBaseline: 'never', checkWhenShown: true });

  useEffect(() => () => applyState('default', null), []);
}
