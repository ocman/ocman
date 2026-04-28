import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import {
  __evaluateForTests,
  __resetForTests,
  type NotifyShape,
} from './useNotificationNotify';

// We exercise the pure controller (`__evaluateForTests`) rather than the
// React hook or any DOM listeners, matching the pattern in
// usePwaInstall.test.ts / useFaviconNotify (no jsdom, no testing-library).
//
// The controller takes a snapshot of session notify state plus context
// (visibility, permission, baseline) and returns the list of
// notifications it decided to fire. Side effects (actually constructing
// the Notification, calling Notification.requestPermission, etc.) are
// the responsibility of the consumer; tests can drive the decision
// matrix directly.

function s(
  id: string,
  status: string,
  extras: Partial<NotifyShape> = {},
): NotifyShape {
  return { id, status, seen: false, ...extras };
}

describe('useNotificationNotify controller', () => {
  beforeEach(() => {
    __resetForTests();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('emits no notifications when permission is not granted', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting')],
      hidden: true,
      permission: 'default',
      enabled: true,
      baseline: null,
    });
    expect(out).toEqual([]);
  });

  it('emits no notifications when the user has disabled them', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting')],
      hidden: true,
      permission: 'granted',
      enabled: false,
      baseline: null,
    });
    expect(out).toEqual([]);
  });

  it('fires a "completed" notification when tab is hidden and a session is waiting', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting')],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(), // empty baseline = "everything is new"
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ kind: 'completed', sessionId: 'a' });
  });

  it('does NOT fire a "completed" notification while the tab is visible', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting')],
      hidden: false,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(out).toEqual([]);
  });

  it('fires a "prompt" notification regardless of visibility (user-blocking)', () => {
    const visible = __evaluateForTests({
      sessions: [s('a', 'idle', { pendingPermission: true })],
      hidden: false,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(visible).toHaveLength(1);
    expect(visible[0]).toMatchObject({ kind: 'prompt', sessionId: 'a' });

    __resetForTests();
    const hidden = __evaluateForTests({
      sessions: [s('a', 'idle', { pendingQuestion: true })],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(hidden).toHaveLength(1);
    expect(hidden[0]).toMatchObject({ kind: 'prompt', sessionId: 'a' });
  });

  it('does NOT re-fire for sessions whose state matches the baseline', () => {
    // Baseline says "a" was already in waiting state when the tab went
    // hidden — so it's not a *new* event, no notification.
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting')],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map([['a', 'waiting|0|0']]),
    });
    expect(out).toEqual([]);
  });

  it('fires once per session even when a session changes state again', () => {
    const baseline = new Map([['a', 'waiting|0|0']]);
    const after = __evaluateForTests({
      sessions: [s('a', 'error')],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline,
    });
    expect(after).toHaveLength(1);
    expect(after[0].kind).toBe('completed');
  });

  it('skips sessions in non-terminal, non-prompt states', () => {
    // "idle" with no pending prompt is neither completed nor blocking.
    const out = __evaluateForTests({
      sessions: [s('a', 'idle')],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(out).toEqual([]);
  });

  it('treats a pending prompt as higher priority than a completion on the same session', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting', { pendingPermission: true })],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    // Only one notification per session, and it's the prompt variant.
    expect(out).toHaveLength(1);
    expect(out[0].kind).toBe('prompt');
  });

  it('fires for multiple distinct sessions in the same tick', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting'), s('b', 'error')],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(out).toHaveLength(2);
    expect(out.map((n) => n.sessionId).sort()).toEqual(['a', 'b']);
  });

  it('skips already-seen completed sessions', () => {
    const out = __evaluateForTests({
      sessions: [s('a', 'waiting', { seen: true })],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(out).toEqual([]);
  });

  it('still fires a prompt notification even if the session is marked seen', () => {
    // "Seen" tracks the user having opened the session in the UI, but a
    // pending permission/question is a fresh user-blocking event. The
    // favicon hook makes the same distinction.
    const out = __evaluateForTests({
      sessions: [
        s('a', 'idle', { seen: true, pendingPermission: true }),
      ],
      hidden: true,
      permission: 'granted',
      enabled: true,
      baseline: new Map(),
    });
    expect(out).toHaveLength(1);
    expect(out[0].kind).toBe('prompt');
  });
});
