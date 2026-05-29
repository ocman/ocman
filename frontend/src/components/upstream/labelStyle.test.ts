import { describe, expect, it } from 'vitest';
import { normalizeHex, relativeLuminance, styleForLabel } from './labelStyle';

describe('normalizeHex', () => {
  it.each([
    ['fef2c0', 'fef2c0'],
    ['#fef2c0', 'fef2c0'],
    ['FEF2C0', 'fef2c0'],
    ['abc', 'aabbcc'],
    ['#abc', 'aabbcc'],
  ])('normalises %s → %s', (input, want) => {
    expect(normalizeHex(input)).toBe(want);
  });

  it.each(['', 'xyz', '12345', '12345g', 'rgb(1,2,3)'])(
    'returns null for invalid input %s',
    (input) => {
      expect(normalizeHex(input)).toBeNull();
    },
  );
});

describe('relativeLuminance', () => {
  it('returns 0 for pure black', () => {
    expect(relativeLuminance('000000')).toBeCloseTo(0, 5);
  });

  it('returns 1 for pure white', () => {
    expect(relativeLuminance('ffffff')).toBeCloseTo(1, 5);
  });

  it('returns roughly 0.5 for mid-grey', () => {
    // sRGB 808080 is perceptually mid; linear-light luminance is ~0.216
    // (so under the 0.5 cutoff — gets white text, which is desired).
    expect(relativeLuminance('808080')).toBeGreaterThan(0.15);
    expect(relativeLuminance('808080')).toBeLessThan(0.30);
  });

  it('picks higher luminance for yellows than for blues at same brightness', () => {
    // Yellow has higher relative luminance than blue in WCAG.
    expect(relativeLuminance('ffff00')).toBeGreaterThan(relativeLuminance('0000ff'));
  });
});

describe('styleForLabel', () => {
  it('returns undefined when color is empty/null', () => {
    expect(styleForLabel(undefined)).toBeUndefined();
    expect(styleForLabel(null)).toBeUndefined();
    expect(styleForLabel('')).toBeUndefined();
    expect(styleForLabel('not-a-color')).toBeUndefined();
  });

  it('picks dark text on a light background (e.g. pale yellow)', () => {
    const style = styleForLabel('fef2c0');
    expect(style?.backgroundColor).toBe('#fef2c0');
    // Dark text token (Catppuccin --crust).
    expect(style?.color).toBe('#1e1e2e');
  });

  it('picks light text on a dark background (e.g. dark blue)', () => {
    const style = styleForLabel('1d76db');
    expect(style?.backgroundColor).toBe('#1d76db');
    expect(style?.color).toBe('#ffffff');
  });

  it('handles a leading hash and uppercase', () => {
    const style = styleForLabel('#1D76DB');
    expect(style?.backgroundColor).toBe('#1d76db');
    expect(style?.color).toBe('#ffffff');
  });

  it('handles 3-char shorthand', () => {
    const style = styleForLabel('fff');
    expect(style?.backgroundColor).toBe('#ffffff');
    expect(style?.color).toBe('#1e1e2e');
  });

  it('adds a subtle border for pop against the row background', () => {
    expect(styleForLabel('fef2c0')?.border).toMatch(/rgba\(0,0,0/);
    expect(styleForLabel('1d76db')?.border).toMatch(/rgba\(255,255,255/);
  });
});
