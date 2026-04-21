import { describe, expect, it } from 'vitest';
import {
  hardenMessageLinks,
  MESSAGE_LINK_SELECTOR,
  type HardenableAnchor,
  type HardenableRoot,
} from './linkHardener';

// The project doesn't ship with a DOM test environment (no jsdom/happy-dom),
// so we use a tiny fake root that records the selector it was called with
// and returns whatever anchors the test set up. This keeps the test focused
// on the rule — the DOM/MutationObserver wiring in AssistantThread is thin
// glue that's easier to verify by hand.

function makeAnchor(init: Partial<HardenableAnchor> = {}): HardenableAnchor {
  return { target: '', rel: '', ...init };
}

function makeRoot(anchors: HardenableAnchor[]): HardenableRoot & { lastSelector: string | null } {
  const root = {
    lastSelector: null as string | null,
    querySelectorAll(selector: string) {
      root.lastSelector = selector;
      return anchors;
    },
  };
  return root;
}

describe('hardenMessageLinks', () => {
  it('scopes its query to message-body anchors with href', () => {
    const root = makeRoot([]);
    hardenMessageLinks(root);
    expect(root.lastSelector).toBe(MESSAGE_LINK_SELECTOR);
    expect(MESSAGE_LINK_SELECTOR).toBe('.oc-msg-body a[href]');
  });

  it('sets target=_blank and rel=noopener noreferrer on plain anchors', () => {
    const a = makeAnchor();
    hardenMessageLinks(makeRoot([a]));
    expect(a.target).toBe('_blank');
    expect(a.rel).toBe('noopener noreferrer');
  });

  it('overwrites a different target (e.g. _self) with _blank', () => {
    const a = makeAnchor({ target: '_self', rel: '' });
    hardenMessageLinks(makeRoot([a]));
    expect(a.target).toBe('_blank');
    expect(a.rel).toBe('noopener noreferrer');
  });

  it('overwrites an insecure rel value', () => {
    const a = makeAnchor({ target: '_blank', rel: 'opener' });
    hardenMessageLinks(makeRoot([a]));
    expect(a.rel).toBe('noopener noreferrer');
  });

  it('is idempotent — already-hardened anchors are not written to again', () => {
    // Matters because the effect uses a MutationObserver: writing an
    // attribute to its current value would fire another mutation record
    // and loop. We detect "untouched" by counting setter invocations.
    let targetWrites = 0;
    let relWrites = 0;
    const a: HardenableAnchor = {
      get target() { return '_blank'; },
      set target(_v: string) { targetWrites++; },
      get rel() { return 'noopener noreferrer'; },
      set rel(_v: string) { relWrites++; },
    };
    hardenMessageLinks(makeRoot([a]));
    expect(targetWrites).toBe(0);
    expect(relWrites).toBe(0);
  });

  it('handles multiple anchors in one pass', () => {
    const anchors = [
      makeAnchor(),
      makeAnchor({ target: '_parent', rel: '' }),
      makeAnchor({ target: '_blank', rel: '' }),
    ];
    hardenMessageLinks(makeRoot(anchors));
    for (const a of anchors) {
      expect(a.target).toBe('_blank');
      expect(a.rel).toBe('noopener noreferrer');
    }
  });

  it('does nothing when there are no matching anchors', () => {
    const root = makeRoot([]);
    expect(() => hardenMessageLinks(root)).not.toThrow();
  });
});
