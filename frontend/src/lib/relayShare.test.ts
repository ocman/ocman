// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { mergeRelayChunks, relayKeyFromFragment } from './relayShare';
import type { SharedConversation } from './api.types';

const empty: SharedConversation = { session: null, messages: [], parts: [], readOnly: true };

describe('relayShare', () => {
  it('reads the key from the URL fragment', () => {
    expect(relayKeyFromFragment('#k=abc_def-1')).toBe('abc_def-1');
    expect(relayKeyFromFragment('#other=x')).toBe('');
  });

  it('upserts rows from later chunks and keeps them time ordered', () => {
    const first: SharedConversation = {
      ...empty,
      messages: [{ id: 'm2', sessionId: 's', timeCreated: 2, data: { role: 'assistant' } }],
      parts: [{ id: 'p', messageId: 'm2', sessionId: 's', timeCreated: 2, data: { type: 'text', text: 'old' } }],
    };
    const second: SharedConversation = {
      ...empty,
      messages: [{ id: 'm1', sessionId: 's', timeCreated: 1, data: { role: 'user' } }],
      parts: [{ id: 'p', messageId: 'm2', sessionId: 's', timeCreated: 2, data: { type: 'text', text: 'new' } }],
    };
    const got = mergeRelayChunks(null, [first, second]);
    expect(got.messages.map((m) => m.id)).toEqual(['m1', 'm2']);
    expect(got.parts[0].data).toEqual({ type: 'text', text: 'new' });
  });
});
