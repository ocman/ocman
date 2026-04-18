import { describe, expect, it, vi } from 'vitest';
import {
  bindingKeys,
  groupByScope,
  matchShortcut,
  shortcutLabel,
  type Shortcut,
} from './shortcutRegistry';

type FakeKeyEvent = Pick<
  KeyboardEvent,
  'code' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'repeat' | 'defaultPrevented' | 'target'
>;

function makeEvent(overrides: Partial<FakeKeyEvent> & Pick<FakeKeyEvent, 'code'>): FakeKeyEvent {
  return {
    code: overrides.code,
    altKey: overrides.altKey ?? false,
    ctrlKey: overrides.ctrlKey ?? false,
    metaKey: overrides.metaKey ?? false,
    shiftKey: overrides.shiftKey ?? false,
    repeat: overrides.repeat ?? false,
    defaultPrevented: overrides.defaultPrevented ?? false,
    target: overrides.target ?? null,
  };
}

function shortcut(
  partial: Partial<Shortcut> & Pick<Shortcut, 'id' | 'scope' | 'keys'>,
): Shortcut {
  return {
    description: partial.description ?? partial.id,
    handler: partial.handler ?? vi.fn(),
    ...partial,
  };
}

// Most matching tests only care about which shortcut fires, not the
// preventDefault flag — this helper keeps those assertions terse.
function runMatch(shortcuts: Iterable<Shortcut>, e: FakeKeyEvent): Shortcut | null {
  return matchShortcut(shortcuts, e).match;
}

describe('matchShortcut', () => {
  describe('modifier matching', () => {
    it('matches a shortcut with alt when Alt is held', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: true }));
      expect(match).toBe(s);
    });

    it('does not match when Alt is missing', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: false }));
      expect(match).toBeNull();
    });

    it('does not match when an unexpected modifier is held', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      // Ctrl+Alt+J must not fire an Alt+J shortcut — Ctrl is not part of the
      // binding so the event shouldn't match.
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: true, ctrlKey: true }));
      expect(match).toBeNull();
    });

    it('never matches while meta (Cmd) is held', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: true, metaKey: true }));
      expect(match).toBeNull();
    });

    it('matches exact shift state', () => {
      const withShift = shortcut({
        id: 'test.alt-shift-slash',
        scope: 'site',
        keys: { code: 'Slash', alt: true, shift: true },
        label: 'Alt+?',
      });
      const without = shortcut({
        id: 'test.alt-slash',
        scope: 'site',
        keys: { code: 'Slash', alt: true },
        label: 'Alt+/',
      });
      const withShiftEvent = makeEvent({ code: 'Slash', altKey: true, shiftKey: true });
      const withoutShiftEvent = makeEvent({ code: 'Slash', altKey: true, shiftKey: false });
      expect(runMatch([withShift, without], withShiftEvent)).toBe(withShift);
      expect(runMatch([withShift, without], withoutShiftEvent)).toBe(without);
    });
  });

  describe('multiple bindings', () => {
    it('matches any binding in a list', () => {
      const s = shortcut({
        id: 'test.alt-question',
        scope: 'site',
        keys: [
          { code: 'Slash', alt: true, shift: true },
          { code: 'Slash', alt: true },
        ],
        label: 'Alt+?',
      });
      expect(runMatch([s], makeEvent({ code: 'Slash', altKey: true, shiftKey: true }))).toBe(s);
      expect(runMatch([s], makeEvent({ code: 'Slash', altKey: true }))).toBe(s);
      // Unrelated keys still don't match.
      expect(runMatch([s], makeEvent({ code: 'KeyJ', altKey: true }))).toBeNull();
    });
  });

  describe('editable-target gating', () => {
    // Minimal duck-typed stub for the event target — matches what the
    // dispatcher reads off `e.target` without needing a real DOM.
    function editableTarget(): EventTarget {
      return { tagName: 'TEXTAREA', isContentEditable: false } as unknown as EventTarget;
    }

    it('fires Alt shortcuts even while typing in a textarea', () => {
      const s = shortcut({
        id: 'test.alt-d',
        scope: 'composer',
        keys: { code: 'KeyD', alt: true },
        label: 'Alt+D',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyD', altKey: true, target: editableTarget() }));
      // Alt shortcuts default to runInEditable=true so Mac Option doesn't
      // leak ∂ into the field.
      expect(match).toBe(s);
    });

    it('suppresses plain-key shortcuts while typing in a textarea', () => {
      const s = shortcut({
        id: 'test.prompt-one',
        scope: 'prompt',
        keys: { code: 'Digit1' },
        label: '1',
      });
      const match = runMatch([s], makeEvent({ code: 'Digit1', target: editableTarget() }));
      expect(match).toBeNull();
    });

    it('respects an explicit runInEditable: false on an Alt shortcut', () => {
      const s = shortcut({
        id: 'test.alt-x',
        scope: 'session',
        keys: { code: 'KeyX', alt: true },
        label: 'Alt+X',
        runInEditable: false,
      });
      const match = runMatch([s], makeEvent({ code: 'KeyX', altKey: true, target: editableTarget() }));
      expect(match).toBeNull();
    });
  });

  describe('priority', () => {
    it('prefers the narrower scope when two shortcuts share a binding', () => {
      const siteLevel = shortcut({
        id: 'site.x',
        scope: 'site',
        keys: { code: 'KeyX', alt: true },
        label: 'Alt+X (site)',
      });
      const composerLevel = shortcut({
        id: 'composer.x',
        scope: 'composer',
        keys: { code: 'KeyX', alt: true },
        label: 'Alt+X (composer)',
      });
      const match = runMatch([siteLevel, composerLevel], makeEvent({ code: 'KeyX', altKey: true }));
      expect(match).toBe(composerLevel);
    });

    it('lets an explicit priority override the scope default', () => {
      const hi = shortcut({
        id: 'site.hi',
        scope: 'site',
        keys: { code: 'KeyY', alt: true },
        label: 'Alt+Y',
        priority: 999,
      });
      const lo = shortcut({
        id: 'composer.lo',
        scope: 'composer',
        keys: { code: 'KeyY', alt: true },
        label: 'Alt+Y',
      });
      const match = runMatch([lo, hi], makeEvent({ code: 'KeyY', altKey: true }));
      expect(match).toBe(hi);
    });
  });

  describe('enabled predicate', () => {
    it('skips shortcuts whose predicate returns false', () => {
      const disabled = shortcut({
        id: 'session.new',
        scope: 'session',
        keys: { code: 'KeyC', alt: true },
        label: 'Alt+C',
        enabled: () => false,
      });
      const match = runMatch([disabled], makeEvent({ code: 'KeyC', altKey: true }));
      expect(match).toBeNull();
    });

    it('falls back to a lower-priority match when the preferred one is disabled', () => {
      const disabledHi = shortcut({
        id: 'composer.c',
        scope: 'composer',
        keys: { code: 'KeyC', alt: true },
        label: 'Alt+C (composer)',
        enabled: () => false,
      });
      const enabledLo = shortcut({
        id: 'session.c',
        scope: 'session',
        keys: { code: 'KeyC', alt: true },
        label: 'Alt+C (session)',
      });
      const match = runMatch([disabledHi, enabledLo], makeEvent({ code: 'KeyC', altKey: true }));
      expect(match).toBe(enabledLo);
    });
  });

  describe('preventDefault policy', () => {
    // These tests capture the Mac-Option correctness contract: whenever an
    // Alt binding syntactically matches the event, the browser's default
    // action must be suppressed so Option+D doesn't leak ∂ into the focused
    // input — even if the shortcut's enabled() predicate returns false.

    it('requests preventDefault when a shortcut handler will run', () => {
      const s = shortcut({
        id: 'site.ok',
        scope: 'site',
        keys: { code: 'KeyJ', alt: true },
      });
      const outcome = matchShortcut([s], makeEvent({ code: 'KeyJ', altKey: true }));
      expect(outcome.match).toBe(s);
      expect(outcome.preventDefault).toBe(true);
    });

    it('requests preventDefault even when the only matching Alt shortcut is disabled', () => {
      const s = shortcut({
        id: 'composer.dictation',
        scope: 'composer',
        keys: { code: 'KeyD', alt: true },
        enabled: () => false,
      });
      const outcome = matchShortcut([s], makeEvent({ code: 'KeyD', altKey: true }));
      // No handler runs, but the Option character must still be suppressed.
      expect(outcome.match).toBeNull();
      expect(outcome.preventDefault).toBe(true);
    });

    it('does not request preventDefault when nothing matches', () => {
      const s = shortcut({
        id: 'site.other',
        scope: 'site',
        keys: { code: 'KeyX', alt: true },
      });
      const outcome = matchShortcut([s], makeEvent({ code: 'KeyY', altKey: true }));
      expect(outcome.match).toBeNull();
      expect(outcome.preventDefault).toBe(false);
    });

    it('does not request preventDefault for a disabled non-Alt shortcut', () => {
      // Plain-key shortcuts don't have the Mac-Option concern, so we don't
      // swallow the keystroke when they're gated.
      const s = shortcut({
        id: 'prompt.one',
        scope: 'prompt',
        keys: { code: 'Digit1' },
        enabled: () => false,
      });
      const outcome = matchShortcut([s], makeEvent({ code: 'Digit1' }));
      expect(outcome.match).toBeNull();
      expect(outcome.preventDefault).toBe(false);
    });
  });

  describe('event guards', () => {
    it('ignores key-repeat events', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: true, repeat: true }));
      expect(match).toBeNull();
    });

    it('ignores already-prevented events', () => {
      const s = shortcut({
        id: 'test.alt-j',
        scope: 'session',
        keys: { code: 'KeyJ', alt: true },
        label: 'Alt+J',
      });
      const match = runMatch([s], makeEvent({ code: 'KeyJ', altKey: true, defaultPrevented: true }));
      expect(match).toBeNull();
    });
  });
});

describe('groupByScope', () => {
  it('groups shortcuts by scope and sorts by label', () => {
    const shortcuts: Shortcut[] = [
      shortcut({ id: 'b', scope: 'session', keys: { code: 'KeyB' }, label: 'B' }),
      shortcut({ id: 'a', scope: 'session', keys: { code: 'KeyA' }, label: 'A' }),
      shortcut({ id: 'site', scope: 'site', keys: { code: 'KeyS' }, label: 'S' }),
    ];
    const groups = groupByScope(shortcuts);
    expect(groups.session.map((s) => s.id)).toEqual(['a', 'b']);
    expect(groups.site.map((s) => s.id)).toEqual(['site']);
    expect(groups.project).toEqual([]);
    expect(groups.composer).toEqual([]);
    expect(groups.prompt).toEqual([]);
  });

  it('sorts by the derived label when no explicit label is set', () => {
    const shortcuts: Shortcut[] = [
      shortcut({ id: 'zebra', scope: 'session', keys: { code: 'KeyZ', alt: true } }),
      shortcut({ id: 'apple', scope: 'session', keys: { code: 'KeyA', alt: true } }),
    ];
    const groups = groupByScope(shortcuts);
    // 'Alt+A' < 'Alt+Z' on Linux; '⌥+A' < '⌥+Z' on Mac — stable either way.
    expect(groups.session.map((s) => s.id)).toEqual(['apple', 'zebra']);
  });
});

// Switch navigator.platform for a single test body. Works without a real DOM
// because the app's isMacPlatform() helper reads from navigator.platform and
// vitest's default environment already provides a writable navigator.
function withPlatform(platform: string, fn: () => void) {
  const original = Object.getOwnPropertyDescriptor(globalThis.navigator, 'platform');
  Object.defineProperty(globalThis.navigator, 'platform', {
    value: platform,
    configurable: true,
    writable: true,
  });
  try {
    fn();
  } finally {
    if (original) {
      Object.defineProperty(globalThis.navigator, 'platform', original);
    }
  }
}

describe('bindingKeys (display helpers)', () => {
  it('renders Alt+Letter as ["Alt", "J"] on non-Mac platforms', () => {
    withPlatform('Linux x86_64', () => {
      expect(bindingKeys({ code: 'KeyJ', alt: true })).toEqual(['Alt', 'J']);
    });
  });

  it('renders Alt+Letter as ["⌥", "J"] on Mac', () => {
    withPlatform('MacIntel', () => {
      expect(bindingKeys({ code: 'KeyJ', alt: true })).toEqual(['⌥', 'J']);
    });
  });

  it('maps arrow key codes to arrow glyphs', () => {
    withPlatform('Linux x86_64', () => {
      expect(bindingKeys({ code: 'ArrowUp', alt: true })).toEqual(['Alt', '↑']);
      expect(bindingKeys({ code: 'ArrowDown', alt: true })).toEqual(['Alt', '↓']);
    });
  });

  it('renders Shift+Slash as the "?" character rather than an explicit Shift chip', () => {
    withPlatform('Linux x86_64', () => {
      // Alt+Shift+/ → "?" — avoids the user seeing "Alt + Shift + /" when
      // they physically press Alt+?.
      expect(bindingKeys({ code: 'Slash', alt: true, shift: true })).toEqual(['Alt', '?']);
    });
  });

  it('shows Shift explicitly when it modifies a non-punctuation key', () => {
    withPlatform('Linux x86_64', () => {
      expect(bindingKeys({ code: 'KeyK', alt: true, shift: true })).toEqual(['Alt', 'Shift', 'K']);
    });
    withPlatform('MacIntel', () => {
      expect(bindingKeys({ code: 'KeyK', alt: true, shift: true })).toEqual(['⌥', '⇧', 'K']);
    });
  });

  it('renders a bare key without any modifier chips', () => {
    withPlatform('Linux x86_64', () => {
      expect(bindingKeys({ code: 'Space' })).toEqual(['Space']);
      expect(bindingKeys({ code: 'Digit1' })).toEqual(['1']);
    });
  });
});

describe('shortcutLabel (string fallback)', () => {
  it('returns the explicit label when provided', () => {
    const s = shortcut({
      id: 't',
      scope: 'site',
      keys: { code: 'KeyJ', alt: true },
      label: 'Custom Label',
    });
    expect(shortcutLabel(s)).toBe('Custom Label');
  });

  it('derives a "+"-joined label from the primary binding when no label is set', () => {
    withPlatform('Linux x86_64', () => {
      const s = shortcut({ id: 't', scope: 'site', keys: { code: 'KeyJ', alt: true } });
      expect(shortcutLabel(s)).toBe('Alt+J');
    });
  });

  it('uses the first binding when multiple alternatives are given', () => {
    withPlatform('Linux x86_64', () => {
      const s = shortcut({
        id: 't',
        scope: 'site',
        keys: [
          { code: 'Slash', alt: true, shift: true },
          { code: 'Slash', alt: true },
        ],
      });
      expect(shortcutLabel(s)).toBe('Alt+?');
    });
  });
});
