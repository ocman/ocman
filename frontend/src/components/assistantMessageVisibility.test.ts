import { describe, it, expect } from 'vitest';
import type { Message, Part } from '../lib/api';
import { computeTurnStats } from '../lib/turnStats';
import { shouldRenderAssistantMessage } from './assistantMessageVisibility';

function makeMessage(
  id: string,
  data: Partial<Message['data']> & { role: 'user' | 'assistant' },
  timeCreated = 0,
): Message {
  return { id, sessionId: 's', timeCreated, data: { ...data } };
}

function toolPart(messageId: string): Part {
  return {
    id: `${messageId}-tool`,
    messageId,
    sessionId: 's',
    data: { type: 'tool' } as unknown as string,
  };
}

describe('shouldRenderAssistantMessage', () => {
  it('renders when the message has content, regardless of anchor state', () => {
    expect(shouldRenderAssistantMessage(true, false)).toBe(true);
    expect(shouldRenderAssistantMessage(true, true)).toBe(true);
  });

  it('keeps an empty message mounted while it is the live summary anchor', () => {
    // This is the turn-line flicker fix: an empty-but-live anchor row
    // owns the turn summary line, so it must stay rendered while data
    // streams in.
    expect(shouldRenderAssistantMessage(false, true)).toBe(true);
  });

  it('skips an empty message that is not the live summary anchor', () => {
    expect(shouldRenderAssistantMessage(false, false)).toBe(false);
  });
});

describe('computeTurnStats — summary anchor', () => {
  it('anchors the turn on the last assistant message', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant', finish: 'tool-calls' }, 2),
      makeMessage('a2', { role: 'assistant' }, 3),
    ];
    const map = computeTurnStats(messages, []);

    // Every assistant message in the turn carries the aggregate...
    expect(map.get('a1')).toBeDefined();
    expect(map.get('a2')).toBeDefined();
    // ...but only the last one is the anchor that renders the bar.
    expect(map.get('a1')?.isSummaryAnchor).toBe(false);
    expect(map.get('a2')?.isSummaryAnchor).toBe(true);
  });

  it('keeps the turn line visible across a tool-call step (the bug)', () => {
    // Mid-turn during a tool call, OpenCode appends a fresh trailing
    // assistant message. The previous message is no longer the anchor
    // but must still carry the aggregate, and the new (possibly empty)
    // message becomes the live anchor — so the line never blanks out.
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant', finish: 'tool-calls' }, 2),
      makeMessage('a2', { role: 'assistant' }, 3), // new, still streaming
    ];
    const map = computeTurnStats(messages, [toolPart('a1')]);

    const prev = map.get('a1');
    const next = map.get('a2');

    // Previous message: has aggregate, not anchor → renders no bar but
    // does not lose its turn data.
    expect(prev?.toolCalls).toBe(1);
    expect(prev?.isSummaryAnchor).toBe(false);

    // New trailing message: live anchor with the same aggregate.
    expect(next?.isLive).toBe(true);
    expect(next?.isSummaryAnchor).toBe(true);
    expect(next?.toolCalls).toBe(1);

    // The empty, live anchor stays mounted so the bar is continuous.
    const isLiveAnchor = (next?.isLive && next?.isSummaryAnchor) ?? false;
    expect(shouldRenderAssistantMessage(false, isLiveAnchor)).toBe(true);
  });
});

describe('turn line lifecycle', () => {
  it('marks a trailing empty assistant message as the live anchor', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant' }, 2),
    ];
    const stats = computeTurnStats(messages, []).get('a');

    expect(stats?.isLive).toBe(true);
    expect(stats?.isSummaryAnchor).toBe(true);
    const isLiveAnchor = (stats?.isLive && stats?.isSummaryAnchor) ?? false;
    expect(shouldRenderAssistantMessage(false, isLiveAnchor)).toBe(true);
  });

  it('stops rendering an empty assistant message after the turn finishes', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant', finish: 'stop' }, 2),
    ];
    const stats = computeTurnStats(messages, []).get('a');

    expect(stats?.isLive).toBe(false);
    expect(stats?.isSummaryAnchor).toBe(true);
    const isLiveAnchor = (stats?.isLive && stats?.isSummaryAnchor) ?? false;
    expect(shouldRenderAssistantMessage(false, isLiveAnchor)).toBe(false);
  });
});
