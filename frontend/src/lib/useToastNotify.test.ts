import { describe, it, expect, beforeEach } from 'vitest';
import {
  __evaluateForTests,
  __resetForTests,
  type ToastNotifyShape,
} from './useToastNotify';

// Pure controller tests, mirroring useNotificationNotify.test.ts. We
// drive the decision matrix directly rather than mounting the hook.

function s(
  id: string,
  extras: Partial<ToastNotifyShape> = {},
): ToastNotifyShape {
  return { id, status: 'busy', seen: false, ...extras };
}

describe('useToastNotify controller', () => {
  beforeEach(() => {
    __resetForTests();
  });

  it('emits no toasts for sessions that are not prompting', () => {
    const out = __evaluateForTests({
      sessions: [
        s('a', { status: 'waiting' }), // completed, not blocking
        s('b', { status: 'busy' }),    // working, not blocking
      ],
      currentPath: '/',
      baseline: null,
    });
    expect(out).toEqual([]);
  });

  it('emits a toast for a session with a pending permission', () => {
    const out = __evaluateForTests({
      sessions: [s('a', { pendingPermission: true, title: 'Refactor', directory: '/repo' })],
      currentPath: '/',
      baseline: null,
    });
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({
      sessionId: 'a',
      kind: 'permission',
      title: 'Refactor',
      directory: '/repo',
    });
  });

  it('emits a toast for a session with a pending question', () => {
    const out = __evaluateForTests({
      sessions: [s('a', { pendingQuestion: true })],
      currentPath: '/',
      baseline: null,
    });
    expect(out).toHaveLength(1);
    expect(out[0].kind).toBe('question');
  });

  it('suppresses toasts for the session currently being viewed', () => {
    const out = __evaluateForTests({
      sessions: [s('a', { pendingQuestion: true })],
      currentPath: '/session/a',
      baseline: null,
    });
    expect(out).toEqual([]);
  });

  it('still toasts for *other* sessions while viewing one session', () => {
    const out = __evaluateForTests({
      sessions: [
        s('a', { pendingQuestion: true }),
        s('b', { pendingPermission: true }),
      ],
      currentPath: '/session/a',
      baseline: null,
    });
    expect(out).toHaveLength(1);
    expect(out[0].sessionId).toBe('b');
  });

  it('skips sessions whose state matches the baseline', () => {
    // Baseline says session a was already prompting at mount time.
    // We don't want to fire a toast for a prompt that was *already*
    // there before the user opened the dashboard.
    const baseline = new Map<string, string>([
      ['a', 'busy|0|1'], // status|perm|question
    ]);
    const out = __evaluateForTests({
      sessions: [s('a', { pendingQuestion: true })],
      currentPath: '/',
      baseline,
    });
    expect(out).toEqual([]);
  });

  it('fires when the prompt state changes vs the baseline', () => {
    // Baseline saw session a as busy without a prompt; now it's
    // asking a question, which is genuinely new.
    const baseline = new Map<string, string>([['a', 'busy|0|0']]);
    const out = __evaluateForTests({
      sessions: [s('a', { pendingQuestion: true })],
      currentPath: '/',
      baseline,
    });
    expect(out).toHaveLength(1);
  });

  it('dedupes the same prompt across multiple ticks', () => {
    const sessions = [s('a', { pendingQuestion: true })];
    const first = __evaluateForTests({ sessions, currentPath: '/', baseline: null });
    const second = __evaluateForTests({ sessions, currentPath: '/', baseline: null });
    expect(first).toHaveLength(1);
    // Second tick sees the same state — no second toast for the same prompt.
    expect(second).toEqual([]);
  });

  it('re-fires after a prompt clears and returns', () => {
    const promptingSessions = [s('a', { pendingQuestion: true })];
    const idleSessions = [s('a')];
    __evaluateForTests({ sessions: promptingSessions, currentPath: '/', baseline: null });
    __evaluateForTests({ sessions: idleSessions, currentPath: '/', baseline: null });
    const third = __evaluateForTests({ sessions: promptingSessions, currentPath: '/', baseline: null });
    // Once dedupe is committed it's pinned to the *current* state key.
    // When the state goes back to "no prompt" then forward to "prompt"
    // again, we treat that as a brand-new prompt and toast again.
    expect(third).toHaveLength(1);
  });

  it('prefers permission over question when both are flagged on the same session', () => {
    // Defensive: if a session somehow has both flags, only one toast
    // should be emitted, and we pick "permission" since it tends to
    // be the more user-visible blocking event.
    const out = __evaluateForTests({
      sessions: [s('a', { pendingPermission: true, pendingQuestion: true })],
      currentPath: '/',
      baseline: null,
    });
    expect(out).toHaveLength(1);
    expect(out[0].kind).toBe('permission');
  });
});
