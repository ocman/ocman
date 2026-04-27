import { describe, expect, it } from 'vitest';
import { simpleDiff } from './diff';

// simpleDiff has two code paths:
//   - small inputs (m + n <= 200): full LCS, produces context lines
//     and "..." gaps.
//   - large inputs (m + n > 200): bulk all-removed/all-added without
//     trying to align them.
// Both branches must emit the same numbered output format the diff
// renderer's parseDiff() recognises ("<oldLn>  <newLn>  <op> <text>").

describe('simpleDiff (LCS branch)', () => {
  it('returns empty for identical inputs', () => {
    expect(simpleDiff('hello\n', 'hello\n')).toBe('');
  });

  it('includes a + line for an addition', () => {
    const out = simpleDiff('a\nb\n', 'a\nb\nc\n');
    // A "+ c" row should appear, and it should match the row regex.
    expect(out).toMatch(/\+ c\b/);
    expect(out).toMatch(/^\s*\d*\s{2}\s*\d*\s{2}[+ -] /m);
  });

  it('includes a - line for a deletion', () => {
    const out = simpleDiff('a\nb\nc\n', 'a\nc\n');
    expect(out).toMatch(/- b\b/);
  });

  it('produces line numbers that match the renderer regex', () => {
    const out = simpleDiff('one\ntwo\n', 'one\nTWO\n');
    // Every non-empty line must be parseable by the renderer's regex.
    const re = /^(\s*\d*)\s{2}(\s*\d*)\s{2}([+ -])\s(.*)$/;
    for (const line of out.split('\n')) {
      if (line.trim() === '') continue;
      if (line.trim() === '...') continue;
      expect(line).toMatch(re);
    }
  });
});

describe('simpleDiff (large-input branch)', () => {
  // m + n > 200 triggers the bulk path. Build a 150-line "before"
  // and 150-line "after" so the threshold trips.
  const oldStr = Array.from({ length: 150 }, (_, i) => `old line ${i}`).join('\n');
  const newStr = Array.from({ length: 150 }, (_, i) => `new line ${i}`).join('\n');

  it('emits all old lines as deletions', () => {
    const out = simpleDiff(oldStr, newStr);
    const dashLines = out.split('\n').filter((l) => /\s-\s/.test(l));
    expect(dashLines.length).toBeGreaterThanOrEqual(150);
  });

  it('emits all new lines as additions', () => {
    const out = simpleDiff(oldStr, newStr);
    const plusLines = out.split('\n').filter((l) => /\s\+\s/.test(l));
    expect(plusLines.length).toBeGreaterThanOrEqual(150);
  });

  it('produces output the renderer regex can read (no broken format)', () => {
    // Regression test for the bug we just fixed: the large-input
    // branch used to emit "- foo" / "+ foo" without line numbers,
    // which parseDiff couldn't parse, so big new files rendered as
    // "(no changes)".
    const out = simpleDiff('', 'a\nb\nc\n'.repeat(80));
    const re = /^(\s*\d*)\s{2}(\s*\d*)\s{2}([+ -])\s(.*)$/;
    let matched = 0;
    for (const line of out.split('\n')) {
      if (line.trim() === '') continue;
      if (re.test(line)) matched++;
    }
    expect(matched).toBeGreaterThan(0);
  });
});
