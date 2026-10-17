import { describe, expect, it } from 'vitest';
import type { Message, Part, PartData } from './api';
import { createConvertMessages } from './convertMessages';

const instructions = 'Instructions from: /repo/.claude/worktrees/task/AGENTS.md\n# AGENTS.md\n\nProject instructions';
const message: Message = { id: 'user', sessionId: 's', timeCreated: 1, data: { role: 'user' } };

function part(id: string, data: PartData, serialized = false): Part {
  return { id, messageId: 'user', sessionId: 's', data: serialized ? JSON.stringify(data) : data };
}

describe('injected project instructions', () => {
  it.each([false, true])('omits the synthetic AGENTS.md user bubble, serialized=%s', (serialized) => {
    const parts = [part('context', { type: 'text', text: instructions, synthetic: true }, serialized)];
    expect(createConvertMessages()([message], parts)).toEqual([]);
  });

  it('handles Windows paths and CRLF instruction headers', () => {
    expect(createConvertMessages()([message], [part('context', {
      type: 'text', text: 'Instructions from: C:\\repo\\AGENTS.md\r\nProject rules', synthetic: true,
    })])).toEqual([]);
  });

  it('keeps a real prompt beside injected context', () => {
    const parts = [
      part('context', { type: 'text', text: instructions, synthetic: true }),
      part('prompt', { type: 'text', text: 'Fix the layout' }),
    ];
    expect(createConvertMessages()([message], parts)[0].content).toBe('Fix the layout');
  });

  it.each([
    { type: 'text', text: instructions, synthetic: false },
    { type: 'text', text: instructions },
    { type: 'text', text: '<shell state="completed">Finished</shell>', synthetic: true },
    { type: 'text', text: 'Please read AGENTS.md', synthetic: true },
    { type: 'text', text: 'Instructions from: /repo/notes.md\nNotes', synthetic: true },
    { type: 'text', text: 'Instructions from: /repo/AGENTS.md.backup\nNotes', synthetic: true },
    { type: 'text', text: 'Instructions from: /repo/AGENTS.md', synthetic: true },
  ] satisfies PartData[])('preserves other text: $text', (data) => {
    expect(createConvertMessages()([message], [part('text', data)])[0].content).toBe(data.text);
  });

  it('preserves assistant text even when it quotes injected instructions', () => {
    const assistant: Message = { ...message, data: { role: 'assistant', finish: 'stop' } };
    expect(createConvertMessages()([assistant], [part('quote', {
      type: 'text', text: instructions, synthetic: true,
    })])[0].content).toBe(instructions);
  });

  it('preserves attachments alongside injected instructions', () => {
    const result = createConvertMessages()([message], [
      part('context', { type: 'text', text: instructions, synthetic: true }),
      part('image', { type: 'file', mime: 'image/png', url: 'data:image/png;base64,AA==' }),
    ]);
    expect(result[0].content).toEqual([{ type: 'image', image: 'data:image/png;base64,AA==' }]);
  });

  it('does not hide a user message whose parts have not arrived yet', () => {
    expect(createConvertMessages()([message], [])).toHaveLength(1);
  });

  it('updates visibility as live parts arrive without changing the transcript', () => {
    const convert = createConvertMessages();
    const context = part('context', { type: 'text', text: instructions, synthetic: true });
    expect(convert([message], [])).toHaveLength(1);
    expect(convert([message], [context])).toEqual([]);
    expect(convert([message], [context, part('prompt', { type: 'text', text: 'Continue' })])[0].content).toBe('Continue');
    expect(context.data).toEqual({ type: 'text', text: instructions, synthetic: true });
  });
});
