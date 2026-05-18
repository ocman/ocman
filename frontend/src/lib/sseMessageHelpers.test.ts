import { describe, it, expect } from 'vitest';
import { MAX_OUTPUT_LEN, truncatePartField } from './sseMessageHelpers';

describe('truncatePartField', () => {
  it('returns short strings unchanged', () => {
    expect(truncatePartField('hello')).toBe('hello');
  });

  it('truncates long strings and appends the marker', () => {
    const long = 'x'.repeat(MAX_OUTPUT_LEN + 50);
    const result = truncatePartField(long);
    expect(typeof result).toBe('string');
    expect((result as string).length).toBe(MAX_OUTPUT_LEN + '\n... (truncated)'.length);
    expect((result as string).endsWith('\n... (truncated)')).toBe(true);
  });

  it('returns non-strings unchanged', () => {
    expect(truncatePartField(42)).toBe(42);
    expect(truncatePartField(null)).toBe(null);
    expect(truncatePartField(undefined)).toBe(undefined);
    const obj = { foo: 'bar' };
    expect(truncatePartField(obj)).toBe(obj);
  });

  it('does not truncate at exactly MAX_OUTPUT_LEN', () => {
    const exact = 'x'.repeat(MAX_OUTPUT_LEN);
    expect(truncatePartField(exact)).toBe(exact);
  });
});
