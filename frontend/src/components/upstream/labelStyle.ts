// labelStyle computes a (background, foreground) pair for a forge
// label so the chip text stays readable regardless of the label's
// raw hex color. Forges store the background color only (e.g.
// "f9d0c4" or "1d76db"); the foreground is up to the client.
//
// Algorithm matches GitHub's own label rendering:
//   1. Parse the hex (`#xxxxxx`, `xxxxxx`, or shorthand `xxx`).
//   2. Convert to linear sRGB and compute WCAG relative luminance.
//   3. Pick white text for dark backgrounds, near-black for light.
//
// The threshold (0.5) is the simplest binary choice; for typical
// forge palettes (saturated mid-tones) it agrees with GitHub's
// observed behaviour. We deliberately don't try to be cleverer
// (e.g. computing contrast ratios for both candidates and picking
// the winner) — the cost is one chip with mediocre contrast on
// edge-case colours like pure gray, which the user can recolour
// upstream if it matters.

import type { CSSProperties } from 'react';

const DARK_TEXT = '#1e1e2e'; // matches --crust / Catppuccin background
const LIGHT_TEXT = '#ffffff';

/**
 * styleForLabel returns CSSProperties to apply to a label chip.
 * Returns undefined when no color is available; callers fall back
 * to the default `--surface1` background defined in CSS.
 */
export function styleForLabel(color: string | undefined | null): CSSProperties | undefined {
  if (!color) return undefined;
  const hex = normalizeHex(color);
  if (!hex) return undefined;
  const lum = relativeLuminance(hex);
  return {
    backgroundColor: `#${hex}`,
    color: lum > 0.5 ? DARK_TEXT : LIGHT_TEXT,
    // A tiny semi-transparent border picks the chip out from
    // matching background sections (e.g. white-ish label on the
    // expanded row's mantle background).
    border: `1px solid rgba(${lum > 0.5 ? '0,0,0' : '255,255,255'}, 0.15)`,
  };
}

/**
 * normalizeHex turns user input into a canonical 6-char lowercase
 * hex string with no leading '#'. Returns null when the input
 * doesn't look like a hex color.
 */
export function normalizeHex(raw: string): string | null {
  const trimmed = raw.trim().replace(/^#/, '').toLowerCase();
  if (/^[0-9a-f]{6}$/.test(trimmed)) return trimmed;
  if (/^[0-9a-f]{3}$/.test(trimmed)) {
    // Expand "abc" → "aabbcc".
    return trimmed
      .split('')
      .map((c) => c + c)
      .join('');
  }
  return null;
}

/**
 * relativeLuminance returns the WCAG relative luminance of an
 * sRGB color in [0, 1]. Used to decide between dark and light
 * foreground text for a given background.
 *
 * https://www.w3.org/TR/WCAG20/#relativeluminancedef
 */
export function relativeLuminance(hex: string): number {
  const r = parseInt(hex.slice(0, 2), 16) / 255;
  const g = parseInt(hex.slice(2, 4), 16) / 255;
  const b = parseInt(hex.slice(4, 6), 16) / 255;
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

function linearize(c: number): number {
  return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}
