import { describe, it, expect } from 'vitest';
import { cleanTitle, renderModel, shortSessionID } from './format';

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

describe('shortSessionID', () => {
  it('returns short ids unchanged (length ≤ 12)', () => {
    expect(shortSessionID('abc')).toBe('abc');
    expect(shortSessionID('123456789012')).toBe('123456789012');
  });

  it('compresses long ids to first-4 + ... + last-8', () => {
    expect(shortSessionID('1234567890abcdef0123456789')).toBe('1234...23456789');
  });
});
