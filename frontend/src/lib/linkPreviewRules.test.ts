import { expect, it } from 'vitest';
import { extractCustomLinks } from './linkPreviewRules';

it('expands matched text and capture groups, deduplicates links, and ignores unsafe destinations', () => {
  expect(extractCustomLinks('ABC-42 ABC-42 ABC-7', [
    { pattern: 'ABC-\\d+', replacement: 'https://tracker.example.com/issues/$&' },
    { pattern: 'ABC-(\\d+)', replacement: 'https://example.com/$1' },
    { pattern: 'ABC-\\d+', replacement: 'javascript:alert($&)' },
  ])).toEqual([
    { label: 'ABC-42', url: 'https://tracker.example.com/issues/ABC-42' },
    { label: 'ABC-7', url: 'https://tracker.example.com/issues/ABC-7' },
    { label: 'ABC-42', url: 'https://example.com/42' },
    { label: 'ABC-7', url: 'https://example.com/7' },
  ]);
});
