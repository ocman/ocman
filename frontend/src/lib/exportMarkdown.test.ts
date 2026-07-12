import { describe, it, expect } from 'vitest';
import type { Message, Part } from './api';
import { serializeSessionMarkdown, exportFilename } from './exportMarkdown';

function msg(id: string, role: string): Message {
  return { id, sessionId: 's', timeCreated: 0, data: { role } };
}
function textPart(id: string, messageId: string, text: string, type = 'text'): Part {
  return { id, messageId, sessionId: 's', data: JSON.stringify({ type, text }) };
}

describe('serializeSessionMarkdown', () => {
  it('renders a titled transcript with user/assistant text blocks', () => {
    const messages = [msg('m1', 'user'), msg('m2', 'assistant')];
    const parts = [
      textPart('p1', 'm1', 'Hello there'),
      textPart('p2', 'm2', 'Hi, how can I help?'),
    ];
    const md = serializeSessionMarkdown('My Session', messages, parts);
    expect(md).toContain('# My Session');
    expect(md).toContain('## User\n\nHello there');
    expect(md).toContain('## Assistant\n\nHi, how can I help?');
  });

  it('accepts already-decoded object part data', () => {
    const messages = [msg('m1', 'user')];
    const parts: Part[] = [{ id: 'p1', messageId: 'm1', sessionId: 's', data: { type: 'text', text: 'obj' } }];
    expect(serializeSessionMarkdown(undefined, messages, parts)).toContain('obj');
  });

  it('includes reasoning parts and skips other part types', () => {
    const messages = [msg('m1', 'assistant')];
    const parts = [
      textPart('p1', 'm1', 'thinking...', 'reasoning'),
      textPart('p2', 'm1', 'ignored', 'tool'),
      textPart('p3', 'm1', 'answer', 'text'),
    ];
    const md = serializeSessionMarkdown('t', messages, parts);
    expect(md).toContain('thinking...');
    expect(md).toContain('answer');
    expect(md).not.toContain('ignored');
  });

  it('skips messages with no renderable text and non-conversational roles', () => {
    const messages = [msg('m1', 'user'), msg('m2', 'assistant'), msg('m3', 'system')];
    const parts = [textPart('p1', 'm1', 'only this')];
    const md = serializeSessionMarkdown('t', messages, parts);
    expect(md).toContain('## User');
    expect(md).not.toContain('## Assistant');
    expect(md).not.toContain('system');
  });

  it('falls back to a default title', () => {
    expect(serializeSessionMarkdown('   ', [], [])).toContain('# Session transcript');
  });

  it('tolerates malformed JSON part data', () => {
    const messages = [msg('m1', 'user')];
    const parts: Part[] = [{ id: 'p1', messageId: 'm1', sessionId: 's', data: '{not json' }];
    const md = serializeSessionMarkdown('t', messages, parts);
    expect(md).toContain('# t');
    expect(md).not.toContain('## User');
  });
});

describe('exportFilename', () => {
  it('slugifies the title', () => {
    expect(exportFilename('Fix the Bug!')).toBe('session-fix-the-bug.md');
  });
  it('falls back when title is empty or unslugifiable', () => {
    expect(exportFilename(undefined)).toBe('session-session.md');
    expect(exportFilename('!!!')).toBe('session-session.md');
  });
});
