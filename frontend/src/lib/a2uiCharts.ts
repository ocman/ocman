import type { Component, ValueHint } from './a2ui';

/** Fixed categorical slots from tokens.css, in assignment order. */
const SLOT_COLORS = ['var(--series-1)', 'var(--series-2)', 'var(--series-3)', 'var(--series-4)',
  'var(--series-5)', 'var(--series-6)', 'var(--series-7)', 'var(--series-8)'];
export const SERIES_SLOTS = SLOT_COLORS.length;
/** Folded and agent-named "Other" slices and series are always neutral. */
export const OTHER = 'Other';
/** Pies show at most this many slices, counting the folded Other slice. */
const MAX_SLICES = 8;
/** Slices below this share fold into Other when at least two qualify. */
const SMALL_SHARE = 0.02;

/** CSS color for a palette slot, or the neutral Other color for -1. */
export function slotColor(slot: number): string {
  return SLOT_COLORS[slot] ?? 'var(--series-other)';
}

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

/** Format a chart value with its unit and format hint. */
export function formatValue(value: number, { unit, format = 'number' }: ValueHint = {}): string {
  if (format === 'percent') return `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}%`;
  if (format === 'bytes') {
    let scaled = Math.abs(value), step = 0;
    while (scaled >= 1024 && step < BYTE_UNITS.length - 1) {
      scaled /= 1024;
      step++;
    }
    const digits = step === 0 ? 0 : scaled < 10 ? 2 : 1;
    return `${value < 0 ? '-' : ''}${scaled.toLocaleString(undefined, { maximumFractionDigits: digits })} ${BYTE_UNITS[step]}`;
  }
  const text = value.toLocaleString(undefined, format === 'compact'
    ? { notation: 'compact', maximumFractionDigits: 1 } : { maximumFractionDigits: 2 });
  return unit ? `${text} ${unit}` : text;
}

export type Slice = { label: string; value: number };

/** Visible pie slices: largest first, with small and overflow slices folded into Other. */
export function foldPie(data: Slice[]): Slice[] {
  const total = data.reduce((sum, item) => sum + item.value, 0);
  const sorted = data.filter(item => item.value > 0).sort((a, b) => b.value - a.value);
  let keep = sorted.filter(item => item.label !== OTHER);
  const small = keep.filter(item => item.value / total < SMALL_SHARE);
  if (small.length >= 2) keep = keep.filter(item => !small.includes(item));
  if (keep.length > (keep.length < sorted.length ? MAX_SLICES - 1 : MAX_SLICES)) keep = keep.slice(0, MAX_SLICES - 1);
  const folded = sorted.filter(item => !keep.includes(item));
  return folded.length ? [...keep, { label: OTHER, value: folded.reduce((sum, item) => sum + item.value, 0) }] : keep;
}

/** Names a chart colors, in its own order. */
export function chartNames(c: Component): string[] {
  switch (c.component) {
    case 'PieChart': return foldPie(c.data).map(item => item.label);
    case 'MultiBarChart':
    case 'LineChart':
    case 'ScatterChart': return c.series.map(s => s.name);
    default: return [];
  }
}

/** Surface-wide palette slots by first appearance, so an entity keeps its color across charts. */
export function assignSlots(components: Iterable<Component>): Map<string, number> {
  const slots = new Map<string, number>();
  for (const c of components) {
    for (const name of chartNames(c)) {
      if (name !== OTHER && !slots.has(name)) slots.set(name, slots.size);
    }
  }
  return slots;
}

/** Slots for one chart's names. A chart with a name beyond the shared palette uses its own order
 * instead, so no two of its marks share a color. */
export function chartSlots(names: string[], shared: Map<string, number>): number[] {
  const global = names.map(name => name === OTHER ? -1 : shared.get(name) ?? SERIES_SLOTS);
  if (global.every(slot => slot < SERIES_SLOTS)) return global;
  let next = 0;
  return names.map(name => name === OTHER ? -1 : next++);
}
