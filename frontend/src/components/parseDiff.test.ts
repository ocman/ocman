import { describe, expect, it } from 'vitest';
import { parseDiff } from './parseDiff';

// parseDiff reads the output format produced by simpleDiff() — each
// content line looks like "<oldLn>  <newLn>  <op> <text>", with bare
// "..." marking gaps where unchanged context was omitted.
//
// These tests pin parser behaviour for both branches of simpleDiff
// (the LCS branch for small inputs and the bulk-add/remove branch
// for large inputs that we recently fixed to share the same format).

describe('parseDiff', () => {
  it('returns empty for empty input', () => {
    expect(parseDiff('')).toEqual([]);
  });

  it('parses a single change row', () => {
    const rows = parseDiff(' 1   1  + hello world');
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      kind: 'change',
      op: '+',
      oldLn: '1',
      newLn: '1',
      code: 'hello world',
    });
  });

  it('treats "..." lines as separator rows', () => {
    const rows = parseDiff(['...', ' 5   5  + new'].join('\n'));
    expect(rows[0]).toMatchObject({ kind: 'sep', code: '...' });
    expect(rows[1]).toMatchObject({ kind: 'change', op: '+' });
  });

  it('handles deletion rows (no newLn)', () => {
    const rows = parseDiff(' 3      - dropped');
    expect(rows[0]).toMatchObject({
      kind: 'change',
      op: '-',
      oldLn: '3',
      newLn: '',
      code: 'dropped',
    });
  });

  it('handles addition rows (no oldLn)', () => {
    const rows = parseDiff('     7  + added');
    expect(rows[0]).toMatchObject({
      kind: 'change',
      op: '+',
      oldLn: '',
      newLn: '7',
      code: 'added',
    });
  });

  it('skips lines that don\'t match the expected format', () => {
    const rows = parseDiff(['random noise', ' 1   1  + ok'].join('\n'));
    expect(rows).toHaveLength(1);
    expect(rows[0].code).toBe('ok');
  });
});
