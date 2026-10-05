import { describe, expect, it } from 'vitest';
import type { ThreadMessageLike } from '@assistant-ui/react';
import { convertThreadMessage } from './threadMessageAdapter';

describe('convertThreadMessage', () => {
  it('returns messages without bare tool calls unchanged', () => {
    const text: ThreadMessageLike = { role: 'user', content: 'hi' };
    const parts: ThreadMessageLike = { role: 'assistant', content: [{ type: 'text', text: 'ok' }] };
    expect(convertThreadMessage(text)).toBe(text);
    expect(convertThreadMessage(parts)).toBe(parts);
  });

  it('gives tool calls empty args so argsText is not JSON-parsed', () => {
    const args = { a: 1 };
    const m: ThreadMessageLike = {
      role: 'assistant',
      content: [
        { type: 'tool-call', toolCallId: 't1', toolName: 'bash', argsText: 'completed\n$ ls' },
        { type: 'tool-call', toolCallId: 't2', toolName: 'x', args },
        { type: 'text', text: 'done' },
      ],
    };
    const out = convertThreadMessage(m);
    const content = out.content as Extract<ThreadMessageLike['content'], readonly unknown[]>;
    expect(content[0]).toMatchObject({ toolCallId: 't1', argsText: 'completed\n$ ls', args: {} });
    expect(content[1]).toBe((m.content as typeof content)[1]);
    expect(content[2]).toBe((m.content as typeof content)[2]);
  });
});
