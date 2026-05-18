// ANSI escape sequence parser for terminal output rendering.
//
// Handles SGR (Select Graphic Rendition) sequences — colors, bold,
// dim, italic, underline — and strips other control sequences (CSI
// cursor moves, OSC titles, etc.) that would otherwise leak through
// as garbage characters in a non-terminal context.
//
// Output is a flat list of styled segments suitable for React
// rendering. Colors map to CSS variables from tokens.css (Catppuccin)
// so they pick up theme changes. 256-color and truecolor escapes are
// downcast to the nearest basic ANSI color — the goal is "pretty",
// not "true xterm".

export interface AnsiSegment {
  text: string;
  fg?: string;        // foreground color name (basic ANSI) or undefined
  bg?: string;        // background color name or undefined
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

interface SgrState {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

// Map ANSI color numbers (30-37, 40-47, 90-97, 100-107) to logical
// color names. The bright variants (90-97 / 100-107) share names with
// their dim counterparts — most terminal text uses the bright set
// anyway, and our theme palette is single-tone.
const COLOR_NAMES: Record<number, string> = {
  0: 'black',
  1: 'red',
  2: 'green',
  3: 'yellow',
  4: 'blue',
  5: 'magenta',
  6: 'cyan',
  7: 'white',
};

function colorName(code: number): string | undefined {
  // 30-37 / 90-97 = foreground; 40-47 / 100-107 = background.
  // Code passed in is the offset (0-7).
  return COLOR_NAMES[code];
}

// Map 256-color cube indices to the nearest basic name. Values 0-15
// match the standard ANSI palette directly. For 16-231 (6x6x6 cube)
// we decode r/g/b and pick the dominant channel; 232-255 (grayscale
// ramp) maps to black/white based on intensity.
function color256ToBasic(idx: number): string | undefined {
  if (idx < 0 || idx > 255) return undefined;
  if (idx < 8) return COLOR_NAMES[idx];
  if (idx < 16) return COLOR_NAMES[idx - 8]; // bright → same name
  if (idx >= 232) {
    // Grayscale ramp: 232 (black) → 255 (white).
    return idx < 244 ? 'black' : 'white';
  }
  // 6x6x6 RGB cube.
  const c = idx - 16;
  const r = Math.floor(c / 36);
  const g = Math.floor((c % 36) / 6);
  const b = c % 6;
  return rgbToBasic(r, g, b);
}

function rgbToBasic(r: number, g: number, b: number): string {
  // r/g/b each on a 0-5 scale (from the 6-cube). Pick the dominant
  // channel; if two are roughly equal, return the mixed color name.
  const max = Math.max(r, g, b);
  if (max === 0) return 'black';
  const rHi = r === max;
  const gHi = g === max;
  const bHi = b === max;
  if (rHi && gHi && bHi) return 'white';
  if (rHi && gHi) return 'yellow';
  if (gHi && bHi) return 'cyan';
  if (rHi && bHi) return 'magenta';
  if (rHi) return 'red';
  if (gHi) return 'green';
  if (bHi) return 'blue';
  return 'white';
}

// Convert a truecolor (24-bit) tuple to the nearest basic ANSI name.
// Same approach as color256ToBasic: pick the dominant channel(s).
function trueColorToBasic(r: number, g: number, b: number): string {
  // Normalize each channel to 0-5 (6-cube) so rgbToBasic does the heavy
  // lifting. Treat near-equal channels as equal.
  const norm = (v: number) => Math.round((Math.max(0, Math.min(255, v)) / 255) * 5);
  return rgbToBasic(norm(r), norm(g), norm(b));
}

// Apply a single SGR parameter list (the digits between `\x1b[` and
// `m`) to the running style. Handles extended color sub-sequences
// `38;5;n`, `38;2;r;g;b` and the `48;...` background variants.
function applySgr(state: SgrState, params: number[]): SgrState {
  const next: SgrState = { ...state };
  for (let i = 0; i < params.length; i++) {
    const p = params[i];
    if (p === 0) {
      // Reset.
      next.fg = undefined;
      next.bg = undefined;
      next.bold = undefined;
      next.dim = undefined;
      next.italic = undefined;
      next.underline = undefined;
      continue;
    }
    if (p === 1) { next.bold = true; continue; }
    if (p === 2) { next.dim = true; continue; }
    if (p === 3) { next.italic = true; continue; }
    if (p === 4) { next.underline = true; continue; }
    if (p === 22) { next.bold = undefined; next.dim = undefined; continue; }
    if (p === 23) { next.italic = undefined; continue; }
    if (p === 24) { next.underline = undefined; continue; }
    if (p >= 30 && p <= 37) { next.fg = colorName(p - 30); continue; }
    if (p >= 40 && p <= 47) { next.bg = colorName(p - 40); continue; }
    if (p >= 90 && p <= 97) { next.fg = colorName(p - 90); continue; }
    if (p >= 100 && p <= 107) { next.bg = colorName(p - 100); continue; }
    if (p === 39) { next.fg = undefined; continue; }
    if (p === 49) { next.bg = undefined; continue; }
    if (p === 38 || p === 48) {
      // Extended color: 38;5;n (256-color) or 38;2;r;g;b (truecolor).
      const mode = params[i + 1];
      if (mode === 5 && params.length > i + 2) {
        const c = color256ToBasic(params[i + 2]);
        if (p === 38) next.fg = c; else next.bg = c;
        i += 2;
      } else if (mode === 2 && params.length > i + 4) {
        const c = trueColorToBasic(params[i + 2], params[i + 3], params[i + 4]);
        if (p === 38) next.fg = c; else next.bg = c;
        i += 4;
      } else {
        // Unknown sub-mode — skip the rest of this parameter group.
        break;
      }
      continue;
    }
    // Other SGR codes (blink, reverse, etc.) are intentionally ignored.
  }
  return next;
}

// Matches a CSI escape sequence: ESC [ <params> <final-byte>.
// The final byte is in the range 0x40-0x7e; 'm' is the SGR variant
// we care about. Other CSI sequences (cursor move, erase) are
// dropped silently.
//
// Also matches OSC sequences (ESC ] ... BEL or ESC ] ... ESC \) so
// they're stripped rather than appearing as garbage.
const ESC = '\x1b';
// Control characters in these regexes are intentional — we are
// matching terminal escape sequences, which by definition start with
// ESC (0x1b). Disable the lint rule that complains about them.
/* eslint-disable no-control-regex */
const CSI_RE = /\x1b\[([\d;?]*)([\x40-\x7e])/;
const OSC_RE = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/;

// Single-character escapes we strip without further interpretation:
// ESC ( / ESC ) / ESC * / ESC + (charset designators) and a few
// two-byte introducers. Mostly defensive — rare in modern output.
const SHORT_ESC_RE = /\x1b[()*+\-./][\x20-\x7e]/;
/* eslint-enable no-control-regex */

export function parseAnsi(input: string): AnsiSegment[] {
  if (!input) return [];
  if (!input.includes(ESC)) {
    return [{ text: input }];
  }
  const out: AnsiSegment[] = [];
  let state: SgrState = {};
  let i = 0;
  while (i < input.length) {
    const escIdx = input.indexOf(ESC, i);
    if (escIdx === -1) {
      out.push(makeSegment(input.slice(i), state));
      break;
    }
    if (escIdx > i) {
      out.push(makeSegment(input.slice(i, escIdx), state));
    }
    const rest = input.slice(escIdx);
    const csi = CSI_RE.exec(rest);
    if (csi && csi.index === 0) {
      if (csi[2] === 'm') {
        const params = csi[1] === ''
          ? [0]
          : csi[1].split(';').map((s) => parseInt(s, 10) || 0);
        state = applySgr(state, params);
      }
      // Either way, advance past the CSI sequence.
      i = escIdx + csi[0].length;
      continue;
    }
    const osc = OSC_RE.exec(rest);
    if (osc && osc.index === 0) {
      i = escIdx + osc[0].length;
      continue;
    }
    const short = SHORT_ESC_RE.exec(rest);
    if (short && short.index === 0) {
      i = escIdx + short[0].length;
      continue;
    }
    // Lone ESC with nothing recognizable after it: drop the ESC and
    // keep scanning so we don't infinite-loop.
    i = escIdx + 1;
  }
  return mergeAdjacent(out);
}

function makeSegment(text: string, state: SgrState): AnsiSegment {
  return {
    text,
    fg: state.fg,
    bg: state.bg,
    bold: state.bold,
    dim: state.dim,
    italic: state.italic,
    underline: state.underline,
  };
}

// Combine adjacent segments that share style — keeps the output
// concise when escape sequences appear back-to-back without
// intervening text (common in colorized CLI output).
function mergeAdjacent(segs: AnsiSegment[]): AnsiSegment[] {
  const out: AnsiSegment[] = [];
  for (const s of segs) {
    if (!s.text) continue;
    const last = out[out.length - 1];
    if (last && styleEqual(last, s)) {
      last.text += s.text;
      continue;
    }
    out.push(s);
  }
  return out;
}

function styleEqual(a: AnsiSegment, b: AnsiSegment): boolean {
  return a.fg === b.fg
    && a.bg === b.bg
    && !!a.bold === !!b.bold
    && !!a.dim === !!b.dim
    && !!a.italic === !!b.italic
    && !!a.underline === !!b.underline;
}

// True when the segment has any visible styling. Pure-text segments
// can be emitted as plain strings to avoid extra DOM nodes.
export function hasStyle(s: AnsiSegment): boolean {
  return !!(s.fg || s.bg || s.bold || s.dim || s.italic || s.underline);
}

// True when the input contains any ANSI escape sequence we'd render.
export function hasAnsi(input: string): boolean {
  return typeof input === 'string' && input.includes(ESC);
}
