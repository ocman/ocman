import { describe, expect, it } from 'vitest';
import type { Message, Part } from './api';
import { computeTurnStats, createComputeTurnStats } from './turnStats';

const message = (id: string, role: 'user' | 'assistant', timeCreated: number): Message => ({
  id, sessionId: 's', timeCreated,
  data: { role, ...(role === 'assistant' ? { finish: 'stop', tokens: { input: 10, output: 5 } } : {}) },
});
const notice: Message = { ...message('notice', 'user', 1000), data: { role: 'user', synthetic: true } };
const parts: Part[] = [{ id: 'p-notice', sessionId: 's', messageId: 'notice', data: {
  type: 'text', text: '<shell id="sh1" state="completed" command="make lint">\nPassed\n</shell>',
} }];

describe('turn summaries with a paginated-away prompt', () => {
  it.each([false, true])('keeps a summary anchor without a loaded prompt, live=%s', (isRunning) => {
    const messages = [notice, message('a1', 'assistant', 2000), message('a2', 'assistant', 4000)];
    const stats = computeTurnStats(messages, parts, isRunning);
    expect(stats.get('a1')).toMatchObject({ isSummaryAnchor: false, tokensIn: 20, tokensOut: 10 });
    expect(stats.get('a2')).toMatchObject({
      isSummaryAnchor: true, isLive: isRunning, startedAt: 2000,
      wallClockMs: isRunning ? null : 2000,
    });
    expect(stats.has('notice')).toBe(false);
  });

  it('keeps the partial first turn separate from a later real prompt', () => {
    const messages = [message('a1', 'assistant', 2000), message('u2', 'user', 3000), message('a2', 'assistant', 4000)];
    const stats = computeTurnStats(messages, [], true);
    expect(stats.get('a1')).toMatchObject({ isSummaryAnchor: true, isLive: false, startedAt: 2000, tokensOut: 5 });
    expect(stats.get('a2')).toMatchObject({ isSummaryAnchor: true, isLive: true, startedAt: 3000, tokensOut: 5 });
  });

  it('replaces the partial aggregate when loading the original prompt and earlier replies', () => {
    const compute = createComputeTurnStats();
    const tail = [notice, message('a2', 'assistant', 4000)];
    const partial = compute(tail, parts, false);
    expect(partial.get('a2')).toMatchObject({ isSummaryAnchor: true, startedAt: 4000, tokensOut: 5 });
    expect(compute(tail, parts, false)).toBe(partial);
    const complete = compute([message('u', 'user', 500), message('a1', 'assistant', 2000), ...tail], parts, false);
    expect(complete.get('a2')).toMatchObject({ isSummaryAnchor: true, startedAt: 500, tokensOut: 10, wallClockMs: 3500 });
  });

  it('does not invent summaries for empty history or notifications without any replies', () => {
    expect(computeTurnStats([], [], false).size).toBe(0);
    expect(computeTurnStats([notice], parts, false).size).toBe(0);
  });
});
