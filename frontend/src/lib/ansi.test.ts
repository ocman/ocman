import { describe, it, expect } from 'vitest';
import { parseAnsi, hasAnsi, hasStyle } from './ansi';

describe('parseAnsi', () => {
  it('returns a single plain segment for input without escapes', () => {
    expect(parseAnsi('hello world')).toEqual([{ text: 'hello world' }]);
  });

  it('returns an empty list for empty input', () => {
    expect(parseAnsi('')).toEqual([]);
  });

  it('parses a basic foreground color', () => {
    const segs = parseAnsi('\x1b[31mred\x1b[0m');
    expect(segs).toHaveLength(1);
    expect(segs[0].text).toBe('red');
    expect(segs[0].fg).toBe('red');
  });

  it('parses a bright foreground color (90-97)', () => {
    const segs = parseAnsi('\x1b[92mbright green\x1b[0m');
    expect(segs[0].fg).toBe('green');
  });

  it('parses bold and underline modifiers', () => {
    const segs = parseAnsi('\x1b[1mbold\x1b[0m \x1b[4munderline\x1b[0m');
    expect(segs).toEqual([
      { text: 'bold', bold: true },
      { text: ' ' },
      { text: 'underline', underline: true },
    ]);
  });

  it('combines multiple SGR params in one escape', () => {
    const segs = parseAnsi('\x1b[1;33mwarn\x1b[0m');
    expect(segs[0]).toMatchObject({ text: 'warn', bold: true, fg: 'yellow' });
  });

  it('treats bare \\x1b[m as a reset', () => {
    const segs = parseAnsi('\x1b[31mred\x1b[mplain');
    expect(segs).toEqual([
      { text: 'red', fg: 'red' },
      { text: 'plain' },
    ]);
  });

  it('strips empty styled segments', () => {
    const segs = parseAnsi('\x1b[33m\x1b[0m\x1b[31mhi\x1b[0m');
    expect(segs).toEqual([{ text: 'hi', fg: 'red' }]);
  });

  it('merges adjacent segments with the same style', () => {
    const segs = parseAnsi('\x1b[31mfoo\x1b[31mbar\x1b[0m');
    expect(segs).toEqual([{ text: 'foobar', fg: 'red' }]);
  });

  it('handles 256-color foreground escapes', () => {
    // Color index 196 is in the red range of the 6-cube.
    const segs = parseAnsi('\x1b[38;5;196mred256\x1b[0m');
    expect(segs[0].fg).toBe('red');
  });

  it('handles truecolor foreground escapes', () => {
    const segs = parseAnsi('\x1b[38;2;255;0;0mtrue\x1b[0m');
    expect(segs[0].fg).toBe('red');
  });

  it('preserves text across resets', () => {
    const segs = parseAnsi('plain \x1b[32mgreen\x1b[0m trailing');
    expect(segs.map((s) => s.text).join('')).toBe('plain green trailing');
    expect(segs[1].fg).toBe('green');
  });

  it('strips non-SGR CSI sequences (cursor moves, erase)', () => {
    const segs = parseAnsi('before\x1b[2K\x1b[Aafter');
    expect(segs).toEqual([{ text: 'beforeafter' }]);
  });

  it('strips OSC sequences (titles)', () => {
    const segs = parseAnsi('foo\x1b]0;my title\x07bar');
    expect(segs).toEqual([{ text: 'foobar' }]);
  });

  it('handles the terraform-style warning sample', () => {
    // Real-world fragment from `terraform validate`.
    const sample = '\x1b[33m│\x1b[0m \x1b[0m\x1b[1m\x1b[33mWarning: \x1b[0m\x1b[0m\x1b[1mDeprecated Resource\x1b[0m';
    const segs = parseAnsi(sample);
    const text = segs.map((s) => s.text).join('');
    expect(text).toBe('│ Warning: Deprecated Resource');
    // Verify "Warning: " is bold yellow.
    const warning = segs.find((s) => s.text === 'Warning: ');
    expect(warning).toMatchObject({ bold: true, fg: 'yellow' });
    // Verify "Deprecated Resource" is bold without color.
    const deprecated = segs.find((s) => s.text === 'Deprecated Resource');
    expect(deprecated).toMatchObject({ bold: true });
    expect(deprecated?.fg).toBeUndefined();
  });

  it('handles the terraform success sample', () => {
    const sample = '\x1b[32m\x1b[1mSuccess!\x1b[0m The configuration is valid';
    const segs = parseAnsi(sample);
    expect(segs[0]).toMatchObject({ text: 'Success!', bold: true, fg: 'green' });
    expect(segs[1].text).toBe(' The configuration is valid');
  });

  it('does not infinite-loop on a lone ESC', () => {
    const segs = parseAnsi('foo\x1bbar');
    expect(segs.map((s) => s.text).join('')).toBe('foobar');
  });

  it('handles unknown SGR codes gracefully', () => {
    // Code 7 (reverse) is not modeled — should be silently ignored.
    const segs = parseAnsi('\x1b[7mreversed\x1b[0m');
    expect(segs[0].text).toBe('reversed');
  });

  it('reset clears all attributes', () => {
    const segs = parseAnsi('\x1b[1;31mhot\x1b[0mcool');
    expect(segs).toEqual([
      { text: 'hot', bold: true, fg: 'red' },
      { text: 'cool' },
    ]);
  });

  it('keeps background colors separate from foreground', () => {
    const segs = parseAnsi('\x1b[41;37mwarn\x1b[0m');
    expect(segs[0]).toMatchObject({ fg: 'white', bg: 'red' });
  });
});

describe('hasAnsi', () => {
  it('returns true when the input contains an ESC', () => {
    expect(hasAnsi('\x1b[31mred')).toBe(true);
  });

  it('returns false for plain text', () => {
    expect(hasAnsi('no escapes here')).toBe(false);
  });

  it('returns false for non-string inputs', () => {
    expect(hasAnsi(undefined as unknown as string)).toBe(false);
  });
});

describe('hasStyle', () => {
  it('is true when any style flag or color is set', () => {
    expect(hasStyle({ text: 'x', fg: 'red' })).toBe(true);
    expect(hasStyle({ text: 'x', bold: true })).toBe(true);
  });

  it('is false for an unstyled segment', () => {
    expect(hasStyle({ text: 'x' })).toBe(false);
  });
});
