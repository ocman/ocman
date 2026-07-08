import { describe, it, expect } from 'vitest';
import { parseTodos } from './todos';

const item = { content: 'do a thing', status: 'pending', priority: 'high' };

describe('parseTodos', () => {
  it('parses a { todos: [...] } object from argsText', () => {
    expect(parseTodos(JSON.stringify({ todos: [item] }), null)).toEqual([item]);
  });

  it('parses a bare JSON array from argsText', () => {
    expect(parseTodos(JSON.stringify([item]), null)).toEqual([item]);
  });

  it('extracts a JSON array embedded in surrounding text', () => {
    const embedded = `todowrite args:\n${JSON.stringify([item])}\n(done)`;
    expect(parseTodos(embedded, null)).toEqual([item]);
  });

  it('falls back to the result payload when argsText is empty', () => {
    expect(parseTodos('', { todos: [item] })).toEqual([item]);
  });

  it('returns null for an empty list', () => {
    expect(parseTodos(JSON.stringify({ todos: [] }), null)).toBeNull();
  });

  it('returns null when entries lack content/status', () => {
    expect(parseTodos(JSON.stringify([{ foo: 'bar' }]), null)).toBeNull();
  });

  it('returns null for non-JSON garbage', () => {
    expect(parseTodos('not json at all', 'also not json')).toBeNull();
  });
});
