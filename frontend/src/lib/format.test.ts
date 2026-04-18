import { describe, it, expect } from 'vitest';
import { cleanTitle } from './format';

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
