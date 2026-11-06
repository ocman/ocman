// Forces anchors inside message bodies to open in a new tab with safe rel.
//
// Extracted from AssistantThread so the rule can be unit-tested without a
// DOM test environment. The function only touches attributes when they need
// to change, which keeps MutationObserver callbacks idempotent (setting an
// attribute to its current value would otherwise trigger another mutation
// record and loop).

// Minimal anchor shape we actually mutate. Matches HTMLAnchorElement but
// deliberately narrow so tests can pass hand-rolled fakes without a DOM.
export interface HardenableAnchor {
  target: string;
  rel: string;
}

// Minimal query-root shape. HTMLElement satisfies this; so does any object
// exposing a compatible querySelectorAll.
export interface HardenableRoot {
  querySelectorAll(selector: string): Iterable<HardenableAnchor>;
}

export const MESSAGE_LINK_SELECTOR = '.oc-msg-body a[href]';

export function hardenMessageLinks(root: HardenableRoot): void {
  for (const a of root.querySelectorAll(MESSAGE_LINK_SELECTOR)) {
    if (a.target !== '_blank') a.target = '_blank';
    if (a.rel !== 'noopener noreferrer') a.rel = 'noopener noreferrer';
  }
}

/** Observe the rendered thread; return a disposer for its queued work. */
export function observeMessageLinks(thread: HTMLElement): () => void {
  hardenMessageLinks(thread);
  const pending = new Set<Element>();
  let cancel: (() => void) | undefined;
  const apply = () => {
    cancel = undefined;
    for (const root of pending) {
      if (!root.isConnected || !thread.contains(root)) continue;
      // An added wrapper covers its added descendants; scan it only once.
      let parent = root.parentElement;
      while (parent && parent !== thread && !pending.has(parent)) parent = parent.parentElement;
      if (parent && pending.has(parent)) continue;
      if (root.matches(MESSAGE_LINK_SELECTOR)) {
        const anchor = root as HTMLAnchorElement;
        if (anchor.target !== '_blank') anchor.target = '_blank';
        if (anchor.rel !== 'noopener noreferrer') anchor.rel = 'noopener noreferrer';
      }
      hardenMessageLinks(root);
    }
    pending.clear();
  };
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      for (const node of record.addedNodes) {
        if (node instanceof Element) pending.add(node);
      }
    }
    if (!pending.size || cancel) return;
    if (typeof requestIdleCallback === 'function') {
      const handle = requestIdleCallback(apply, { timeout: 200 });
      cancel = () => cancelIdleCallback(handle);
    } else {
      const handle = setTimeout(apply, 200);
      cancel = () => clearTimeout(handle);
    }
  });
  observer.observe(thread, { childList: true, subtree: true });
  return () => { observer.disconnect(); cancel?.(); pending.clear(); };
}
