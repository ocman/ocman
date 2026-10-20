import { describe, expect, it } from 'vitest';
import { explorationKind, explorationRunning, messageExploration, type ExplorationMessage, type ExplorationPart } from './exploration';

const tool = (toolName: string, argsText = '', artifact?: unknown): ExplorationPart => ({
  type: 'tool-call', toolCallId: argsText, toolName, argsText, args: {}, artifact,
});
const message = (id: string, content: ExplorationMessage['content'], role: ExplorationMessage['role'] = 'assistant'): ExplorationMessage => ({
  id, role, content, metadata: { custom: {} },
});

describe('exploration classification', () => {
  it.each([
    ['read', '', 'file'], ['mcp_read', '', 'file'], ['Read', '', 'file'],
    ['grep', '', 'search'], ['mcp_Grep', '', 'search'], ['glob', '', 'search'],
    ['__read__', 'Read src/a.ts [limit=10]', 'file'],
    ['__read__', 'Grep something (*.go)', 'search'], ['__read__', 'Glob **/*.go', 'search'],
    ['__read__', 'Fetch https://example.com', null], ['webfetch', '', null], ['bash', '', null],
    ['__skill__', 'Skill "test"', null], ['__read__', 'unknown', null],
  ])('classifies %s %s as %s', (name, args, kind) => {
    expect(explorationKind(tool(name, args))).toBe(kind);
  });

  it('leaves text and tools with approval footnotes outside groups', () => {
    expect(explorationKind({ type: 'text', text: 'Read a file' })).toBeNull();
    expect(explorationKind(tool('read', 'file', { ocmanApprovals: [{}] }))).toBeNull();
  });

  it.each(['running', 'pending', 'completed', 'error', ''])('detects %s status from metadata or encoded args', status => {
    const active = status === 'running' || status === 'pending';
    expect(explorationRunning(tool('__read__', 'Read a', { explorationStatus: status }))).toBe(active);
    expect(explorationRunning(tool('grep', `${status}\npattern`))).toBe(active);
  });
});

describe('adjacent exploration messages', () => {
  const a = message('a', [tool('__read__', 'Grep pattern')]);
  const b = message('b', [{ type: 'text', text: ' ' }, tool('__read__', 'Read a.go')]);
  const c = message('c', [tool('__read__', 'Read b.go')]);

  it('collects a contiguous run once, preserving source message and call IDs', () => {
    expect(messageExploration([a, b, c], 'a')?.map(entry => entry.messageId)).toEqual(['a', 'b', 'c']);
    expect(messageExploration([a, b, c], 'b')).toEqual([]);
    expect(messageExploration([a, b, c], 'c')).toEqual([]);
    expect(messageExploration([a], 'missing')).toBeNull();
  });

  it.each([
    message('break', [{ type: 'text', text: 'Now editing' }]),
    message('break', [tool('bash', 'completed\nls')]),
    message('break', [tool('__read__', 'Fetch https://example.com')]),
    message('break', [tool('__read__', 'Read user.txt')], 'user'),
    message('break', []),
  ])('does not cross $id boundaries', boundary => {
    expect(messageExploration([a, boundary, c], 'a')).toHaveLength(1);
    expect(messageExploration([a, boundary, c], 'c')).toHaveLength(1);
    expect(messageExploration([boundary], boundary.id)).toBeNull();
  });

  it.each([{ modelChangedTo: 'new-model' }, { timelineAt: 100 }])('respects visible dividers %j', custom => {
    const divided = { ...b, metadata: { custom } };
    expect(messageExploration([a, divided, c], 'a')).toHaveLength(1);
    expect(messageExploration([a, divided, c], 'b')).toHaveLength(2);
  });
});
