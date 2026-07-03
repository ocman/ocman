import { describe, it, expect } from 'vitest';
import { cleanTitle, formatTokensPerSecond, fuzzyMatch, renderModel, shortSessionID } from './format';

describe('fuzzyMatch', () => {
  it('matches contiguous substrings', () => {
    expect(fuzzyMatch('ocman', '/src/ocman')).toBe(true);
  });
  it('matches non-contiguous subsequences in order', () => {
    expect(fuzzyMatch('ocm', '/o/c/m/an')).toBe(true);
    expect(fuzzyMatch('gho', 'github.com/foo')).toBe(true);
  });
  it('is case-insensitive', () => {
    expect(fuzzyMatch('OCMAN', '/src/ocman')).toBe(true);
  });
  it('rejects out-of-order or missing chars', () => {
    expect(fuzzyMatch('nacmo', '/src/ocman')).toBe(false);
    expect(fuzzyMatch('xyz', '/src/ocman')).toBe(false);
  });
  it('empty query matches anything', () => {
    expect(fuzzyMatch('', 'anything')).toBe(true);
  });
});

describe('cleanTitle', () => {
  it('returns empty string for nullish input', () => {
    expect(cleanTitle(null)).toBe('');
    expect(cleanTitle(undefined)).toBe('');
    expect(cleanTitle('')).toBe('');
  });

  it('leaves plain text untouched', () => {
    expect(cleanTitle('Investigate flaky tests')).toBe('Investigate flaky tests');
  });

  it('strips leading ATX heading markers', () => {
    expect(cleanTitle('# Fix login bug')).toBe('Fix login bug');
    expect(cleanTitle('### Deep dive')).toBe('Deep dive');
    expect(cleanTitle('## Refactor auth ##')).toBe('Refactor auth');
  });

  it('strips balanced bold markers', () => {
    expect(cleanTitle('**Important** task')).toBe('Important task');
    expect(cleanTitle('__Critical__ fix')).toBe('Critical fix');
  });

  it('strips unbalanced bold markers', () => {
    expect(cleanTitle('**Unclosed title')).toBe('Unclosed title');
    expect(cleanTitle('Unopened title**')).toBe('Unopened title');
  });

  it('strips italics', () => {
    expect(cleanTitle('*italicized* words')).toBe('italicized words');
    expect(cleanTitle('some _emphasis_ here')).toBe('some emphasis here');
  });

  it('preserves underscores inside identifiers', () => {
    expect(cleanTitle('fix handle_compact_session bug')).toBe('fix handle_compact_session bug');
  });

  it('strips inline code', () => {
    expect(cleanTitle('Fix `useEffect` deps')).toBe('Fix useEffect deps');
  });

  it('strips strikethrough', () => {
    expect(cleanTitle('~~old~~ new approach')).toBe('old new approach');
  });

  it('unwraps markdown links', () => {
    expect(cleanTitle('See [the docs](https://example.com)')).toBe('See the docs');
  });

  it('handles combinations', () => {
    expect(cleanTitle('# **Critical:** `reset()` in [compact](url)')).toBe('Critical: reset() in compact');
  });

  it('trims surrounding whitespace', () => {
    expect(cleanTitle('   **title**   ')).toBe('title');
  });
});

describe('renderModel', () => {
  it('returns "unknown" for empty input', () => {
    expect(renderModel('')).toBe('unknown');
  });

  it('returns the trailing path segment for provider-prefixed ids', () => {
    expect(renderModel('anthropic/claude-opus-4')).toBe('claude-opus-4');
    expect(renderModel('openai/gpt-4o')).toBe('gpt-4o');
  });

  it('returns the input unchanged when there is no slash', () => {
    expect(renderModel('claude-opus-4')).toBe('claude-opus-4');
  });

  it('keeps only the last segment for multi-slash ids', () => {
    expect(renderModel('a/b/c')).toBe('c');
  });
});

describe('formatTokensPerSecond', () => {
  it('rounds values >= 1 to whole numbers', () => {
    expect(formatTokensPerSecond(1)).toBe('1');
    expect(formatTokensPerSecond(1.4)).toBe('1');
    expect(formatTokensPerSecond(1.6)).toBe('2');
    expect(formatTokensPerSecond(42.7)).toBe('43');
    expect(formatTokensPerSecond(199.49)).toBe('199');
  });

  it('keeps one decimal for values below 1 so they don\'t collapse to 0', () => {
    expect(formatTokensPerSecond(0.94)).toBe('0.9');
    expect(formatTokensPerSecond(0.05)).toBe('0.1');
  });

  it('returns "0" for zero or negative inputs', () => {
    expect(formatTokensPerSecond(0)).toBe('0');
    expect(formatTokensPerSecond(-3)).toBe('0');
  });

  it('returns "0" for non-finite inputs', () => {
    expect(formatTokensPerSecond(Number.NaN)).toBe('0');
    expect(formatTokensPerSecond(Number.POSITIVE_INFINITY)).toBe('0');
  });
});

describe('shortSessionID', () => {
  it('returns short ids unchanged (length ≤ 12)', () => {
    expect(shortSessionID('abc')).toBe('abc');
    expect(shortSessionID('123456789012')).toBe('123456789012');
  });

  it('compresses long ids to first-4 + ... + last-8', () => {
    expect(shortSessionID('1234567890abcdef0123456789')).toBe('1234...23456789');
  });
});
