import { describe, expect, it } from 'vitest';
import { initialSessionView, reduceSessionView } from './sessionReducer';

describe('background task counts', () => {
  const event = (count: unknown, sessionID = 's1') => ({
    type: 'sse' as const,
    event: { type: 'ocman.session.background', properties: { sessionID, count } },
  });

  it('tracks absolute counts and ignores other sessions and invalid counts', () => {
    let view = reduceSessionView(initialSessionView('s1'), event(2));
    expect(view.backgroundTaskCount).toBe(2);
    for (const count of [-1, 1.5, '3', null, Infinity]) {
      expect(reduceSessionView(view, event(count))).toBe(view);
    }
    expect(reduceSessionView(view, event(5, 's2'))).toBe(view);
    expect(reduceSessionView(view, event(2))).toBe(view);
    view = reduceSessionView(view, event(0));
    expect(view.backgroundTaskCount).toBe(0);
  });

  it('restores a reconnect snapshot and clears stale counts when absent', () => {
    let view = reduceSessionView(initialSessionView('s1'), event(4));
    view = reduceSessionView(view, { type: 'sse', event: {
      type: 'server.connected', properties: { backgroundTasks: { s1: 2, s2: 9 } },
    } });
    expect(view.backgroundTaskCount).toBe(2);
    view = reduceSessionView(view, { type: 'sse', event: { type: 'server.connected' } });
    expect(view.backgroundTaskCount).toBe(0);
  });

  it('preserves live counts across REST loads, but not navigation', () => {
    const view = reduceSessionView(initialSessionView('s1'), event(3));
    for (const mode of ['replace', 'reconcile'] as const) {
      expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s1'), mode }).backgroundTaskCount).toBe(3);
    }
    expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s2') }).backgroundTaskCount).toBe(0);
  });

  it('clears counts when the turn ends', () => {
    const view = reduceSessionView(initialSessionView('s1'), event(3));
    expect(reduceSessionView(view, { type: 'sse', event: { type: 'session.idle' } }).backgroundTaskCount).toBe(0);
    expect(reduceSessionView(view, { type: 'sse', event: {
      type: 'session.status', properties: { status: { type: 'idle' } },
    } }).backgroundTaskCount).toBe(0);
  });

  it('ignores malformed reconnect counts', () => {
    const view = initialSessionView('s1');
    for (const backgroundTasks of [null, 'invalid', {}, { s1: -2 }, { s1: '2' }]) {
      expect(reduceSessionView(view, { type: 'sse', event: {
        type: 'server.connected', properties: { backgroundTasks },
      } }).backgroundTaskCount).toBe(0);
    }
  });
});
