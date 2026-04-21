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
