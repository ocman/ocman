import { describe, it, expect } from 'vitest';
import { isSessionRelevant, mcpChildIdsOf } from './promptRouting';

describe('isSessionRelevant', () => {
  const PAGE = 'parent';
  const SUBS = new Set(['child1', 'child2']);

  it('treats undefined session ID as parent-scoped (relevant)', () => {
    expect(isSessionRelevant(undefined, PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant(null, PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant('', PAGE, SUBS)).toBe(true);
  });

  it('matches the page session itself', () => {
    expect(isSessionRelevant(PAGE, PAGE, SUBS)).toBe(true);
  });

  it('matches a known subagent', () => {
    expect(isSessionRelevant('child1', PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant('child2', PAGE, SUBS)).toBe(true);
  });

  it('rejects an unrelated session', () => {
    expect(isSessionRelevant('stranger', PAGE, SUBS)).toBe(false);
  });

  it('rejects a subagent not (yet) known to the page', () => {
    expect(isSessionRelevant('child3', PAGE, SUBS)).toBe(false);
  });
});

describe('mcpChildIdsOf', () => {
  // Regression (#268): ocman MCP/worktree children carry a parentID
  // overlaid from state.db child_sessions. Their prompts must be
  // recognised as relevant on the parent page even though they aren't
  // Task-tool subagents (nothing in the parent's parts references them).
  const sessions = [
    { id: 'parent', parentID: '' },
    { id: 'childA', parentID: 'parent' },
    { id: 'childB', parentID: 'parent' },
    { id: 'other', parentID: 'someoneElse' },
    { id: 'orphan' },
  ];

  it('returns the IDs of sessions whose parentID matches the page', () => {
    expect(mcpChildIdsOf('parent', sessions)).toEqual(new Set(['childA', 'childB']));
  });

  it('returns an empty set when the page has no children', () => {
    expect(mcpChildIdsOf('someoneElse', sessions)).toEqual(new Set(['other']));
    expect(mcpChildIdsOf('nobody', sessions)).toEqual(new Set());
  });

  it('tolerates an empty/undefined page id', () => {
    expect(mcpChildIdsOf('', sessions)).toEqual(new Set());
    expect(mcpChildIdsOf(undefined, sessions)).toEqual(new Set());
  });
});
