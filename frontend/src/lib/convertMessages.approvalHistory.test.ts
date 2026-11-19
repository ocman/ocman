import { describe, expect, it } from 'vitest';
import type { Message, Part, PartData } from './api';
import { createConvertMessages } from './convertMessages';

const message = (id: string, role: Message['data']['role'], timeCreated: number): Message => ({
  id, sessionId: 'session', timeCreated, data: { role },
});
const part = (id: string, messageId: string, timeCreated: number, data: PartData): Part => ({
  id, messageId, sessionId: 'session', timeCreated, data,
});
const approval: PartData = {
  type: 'auto-approved', permission: 'edit', approvedBy: 'user',
  metadata: { tool: 'Write', file_path: '/repo/new.ts', content: 'hello' },
  patterns: ['/repo/new.ts'], askedAt: 150, approvedAt: 200,
};
const call = part('write-part', 'older', 100, {
  type: 'tool', tool: 'write', state: { status: 'completed', input: { file_path: '/repo/new.ts', content: 'hello' } },
});
const notice = part('approval-part', 'approval', 200, approval);

describe('approval locks after loading older history', () => {
  it('attaches an edit permission to its Write call when older history arrives', () => {
    const convert = createConvertMessages();
    const recent = message('recent', 'assistant', 300);
    expect(convert([recent], [])).toHaveLength(1);

    const messages = [message('older', 'assistant', 100), message('approval', 'notice', 200), recent];
    const result = convert(messages, [call, notice]);

    expect(result.map((m) => m.id)).toEqual(['older', 'recent']);
    expect(result[0].content).toEqual([expect.objectContaining({
      type: 'tool-call', toolName: 'write', artifact: expect.objectContaining({
        ocmanApprovals: [expect.objectContaining({ permission: 'edit', approvedBy: 'user' })],
      }),
    })]);
    expect(convert(messages, [call, notice])).toBe(result);
  });

  it('does not match an edit permission to an unrelated tool with identical input', () => {
    const result = createConvertMessages()(
      [message('older', 'assistant', 100), message('approval', 'notice', 200)],
      [part('read-part', 'older', 100, {
        type: 'tool', tool: 'read', state: { status: 'completed', input: { file_path: '/repo/new.ts', content: 'hello' } },
      }), notice],
    );
    expect(result.map((m) => m.id)).toEqual(['older', 'approval']);
  });
});
