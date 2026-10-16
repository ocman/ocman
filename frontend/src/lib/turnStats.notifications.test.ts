import { describe, expect, it } from 'vitest';
import type { Message, Part } from './api';
import { computeTurnStats } from './turnStats';

const message = (id: string, role: string, timeCreated: number): Message => ({
  id, sessionId: 's', timeCreated,
  data: { role, ...(role === 'assistant' ? { finish: 'stop', tokens: { input: 10, output: 5 } } : {}) },
});

describe('turn boundaries around injected notifications', () => {
  it.each([
    ['synthetic message', 'Background work finished.', true],
    ['shell completion', '<shell id="sh1" state="completed" command="make lint">\nOK\n</shell>', false],
    ['task completion', '<task-notification><summary>Tests passed</summary><status>completed</status></task-notification>', false],
    ['synthetic part', 'Continue working.', false, true],
    ['subagent completion', '<subagent sessionID="s1" state="completed">\nDone\n</subagent>', false],
  ])('keeps a %s inside the current turn', (_, text, synthetic, syntheticPart = false) => {
    const messages = [message('u', 'user', 1000), message('a1', 'assistant', 2000),
      { ...message('notice', 'user', 3000), data: { role: 'user', synthetic } },
      message('a2', 'assistant', 4000)];
    const parts: Part[] = [{ id: 'p', sessionId: 's', messageId: 'notice',
      data: JSON.stringify({ type: 'text', text, synthetic: syntheticPart }) }];
    const stats = computeTurnStats(messages, parts, false);
    expect([...stats.values()].filter((turn) => turn.isSummaryAnchor)).toHaveLength(1);
    expect(stats.get('a1')?.isSummaryAnchor).toBe(false);
    expect(stats.get('a2')).toMatchObject({ startedAt: 1000, tokensIn: 20, tokensOut: 10 });
  });

  it('keeps the whole turn live until the session settles', () => {
    const messages = [message('u', 'user', 1000), message('a1', 'assistant', 2000),
      { ...message('notice', 'user', 3000), data: { role: 'user', synthetic: true } },
      message('a2', 'assistant', 4000)];
    const stats = computeTurnStats(messages, [], true);
    expect(stats.get('a1')).toMatchObject({ isLive: true, isSummaryAnchor: false });
    expect(stats.get('a2')).toMatchObject({ isLive: true, isSummaryAnchor: true, wallClockMs: null, startedAt: 1000 });
  });

  it('still starts a new turn for a real follow-up with an attachment', () => {
    const messages = [message('u', 'user', 1000), message('a1', 'assistant', 2000),
      message('followup', 'user', 3000), message('a2', 'assistant', 4000)];
    const parts: Part[] = [{ id: 'p', sessionId: 's', messageId: 'followup',
      data: { type: 'file', filename: 'image.png', mime: 'image/png', url: 'data:image/png;base64,AA==' } }];
    const stats = computeTurnStats(messages, parts, false);
    expect([...stats.values()].filter((turn) => turn.isSummaryAnchor)).toHaveLength(2);
    expect(stats.get('a2')?.startedAt).toBe(3000);
  });

  it.each([
    [],
    [{ type: 'text', text: 'Please continue.' }],
    [{ type: 'text', text: 'Example: <task-notification>not a notice</task-notification>' }],
    [{ type: 'text', text: 'injected', synthetic: true }, { type: 'text', text: 'My follow-up' }],
  ])('does not merge genuine prompts with parts %j', (...partsData) => {
    const messages = [message('u', 'user', 1000), message('a1', 'assistant', 2000),
      message('u2', 'user', 3000), message('a2', 'assistant', 4000)];
    const parts: Part[] = partsData.map((data, i) => ({ id: `p${i}`, messageId: 'u2', sessionId: 's', data }));
    const stats = computeTurnStats(messages, parts, false);
    expect(stats.get('a1')?.isSummaryAnchor).toBe(true);
    expect(stats.get('a2')?.startedAt).toBe(3000);
  });
});
