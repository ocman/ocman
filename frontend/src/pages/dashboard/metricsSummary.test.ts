import { describe, expect, it } from 'vitest';
import { cacheBreakdown, costBasis, costNote, errorDetail, tokenBreakdown, tokensProcessed } from './metricsSummary';

const tokens = { inputTokens: 964, outputTokens: 303_300, cacheReadTokens: 99_300_000, cacheWriteTokens: 1_900_000 };

describe('token summaries', () => {
  it('counts cache reads and writes in the processed total', () => {
    expect(tokensProcessed(tokens)).toBe(101_504_264);
  });
  it('spells out each part and omits zeros', () => {
    expect(tokenBreakdown(tokens)).toBe('964 in · 303.3K out · 99.3M cache read · 1.9M cache write');
    expect(tokenBreakdown({ ...tokens, cacheReadTokens: 0, cacheWriteTokens: 0 })).toBe('964 in · 303.3K out');
    expect(tokenBreakdown({ inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 })).toBe('No tokens');
  });
  it('names cache reads and writes', () => {
    expect(cacheBreakdown(5_100_000, 102_300)).toBe('5.1M read · 102.3K written');
  });
});

describe('cost basis', () => {
  it.each([
    [0, 35.4777, 'estimate', 'API-equivalent estimate · $0 billed'],
    [12, 12, 'billed', 'Billed by the provider'],
    [12, 16.3, 'mixed', '$12.00 billed · rest API-equivalent'],
    [0, 0, 'none', 'No cost recorded'],
    [0.001, 0.005, 'mixed', '$0.001 billed · rest API-equivalent'],
    [0.3, 0.1 + 0.2, 'billed', 'Billed by the provider'],
  ] as const)('reported %s, effective %s is %s', (reported, effective, basis, note) => {
    expect(costBasis(reported, effective)).toBe(basis);
    expect(costNote(reported, effective)).toBe(note);
  });
});

describe('errorDetail', () => {
  it('relates errors to completed requests', () => {
    expect(errorDetail(1, 449)).toBe('1 of 449 failed');
    expect(errorDetail(0, 449)).toBe('None of 449 failed');
  });
});
