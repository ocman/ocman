import { describe, it, expect } from 'vitest';
import { isSessionRelevant } from './promptRouting';

describe('isSessionRelevant', () => {
  const PAGE = 'parent';
  const SUBS = new Set(['subagent1', 'subagent2']);

  it('treats undefined session ID as parent-scoped (relevant)', () => {
    expect(isSessionRelevant(undefined, PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant(null, PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant('', PAGE, SUBS)).toBe(true);
  });

  it('matches the page session itself', () => {
    expect(isSessionRelevant(PAGE, PAGE, SUBS)).toBe(true);
  });

  it('matches a known subagent', () => {
    expect(isSessionRelevant('subagent1', PAGE, SUBS)).toBe(true);
    expect(isSessionRelevant('subagent2', PAGE, SUBS)).toBe(true);
  });

  it('rejects an unrelated session', () => {
    expect(isSessionRelevant('stranger', PAGE, SUBS)).toBe(false);
  });

  it('rejects a subagent not (yet) known to the page', () => {
    expect(isSessionRelevant('subagent3', PAGE, SUBS)).toBe(false);
  });
});
