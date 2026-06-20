import { describe, it, expect } from 'vitest';
import type { Message } from '../lib/api';
import { computeTurnStats } from '../lib/turnStats';
import { shouldRenderAssistantMessage } from './assistantMessageVisibility';

function makeMessage(
  id: string,
  data: Partial<Message['data']> & { role: 'user' | 'assistant' },
  timeCreated = 0,
): Message {
  return { id, sessionId: 's', timeCreated, data: { ...data } };
}

describe('shouldRenderAssistantMessage', () => {
  it('renders when the message has content, regardless of live state', () => {
    expect(shouldRenderAssistantMessage(true, false)).toBe(true);
    expect(shouldRenderAssistantMessage(true, true)).toBe(true);
  });

  it('keeps an empty message mounted while the turn is live', () => {
    // This is the turn-line flicker fix: an empty-but-live assistant
    // row owns the turn summary line, so it must stay rendered while
    // data streams in.
    expect(shouldRenderAssistantMessage(false, true)).toBe(true);
  });

  it('skips an empty message once the turn is no longer live', () => {
    expect(shouldRenderAssistantMessage(false, false)).toBe(false);
  });
});

describe('turn line stays visible while streaming', () => {
  it('marks a trailing empty assistant message as live so the gate keeps it mounted', () => {
    // Simulate a turn where the assistant row exists but no content has
    // streamed in yet (no finish reason, no error).
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant' }, 2),
    ];
    const map = computeTurnStats(messages, []);
    const stats = map.get('a');

    expect(stats?.isLive).toBe(true);
    // With no streamed content yet, the gate must still render the row
    // because the turn is live.
    expect(shouldRenderAssistantMessage(false, stats?.isLive ?? false)).toBe(true);
  });

  it('stops rendering an empty assistant message after the turn finishes', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant', finish: 'stop' }, 2),
    ];
    const map = computeTurnStats(messages, []);
    const stats = map.get('a');

    expect(stats?.isLive).toBe(false);
    expect(shouldRenderAssistantMessage(false, stats?.isLive ?? false)).toBe(false);
  });
});
