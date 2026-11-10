import { describe, expect, it } from 'vitest';
import { assignSlots, chartNames, chartSlots, foldPie, formatValue, slotColor } from './a2uiCharts';
import type { Component } from './a2ui';

const slices = (...values: number[]) => values.map((value, i) => ({ label: `s${i}`, value }));

describe('A2UI chart values', () => {
  it.each([
    [1234.567, {}, '1,234.57'],
    [2.5, { unit: 'cores' }, '2.5 cores'],
    [1530, { format: 'compact' as const, unit: 'req/s' }, '1.5K req/s'],
    [42.25, { format: 'percent' as const, unit: 'ignored' }, '42.3%'],
    [512, { format: 'bytes' as const }, '512 B'],
    [1536, { format: 'bytes' as const }, '1.5 KiB'],
    [25769803776, { format: 'bytes' as const }, '24 GiB'],
    [-157286400, { format: 'bytes' as const }, '-150 MiB'],
    [2 ** 70, { format: 'bytes' as const }, '1,048,576 PiB'],
  ])('formats %s with %o as %s', (value, hint, want) => {
    expect(formatValue(value, hint)).toBe(want);
  });
});

describe('pie folding', () => {
  it('keeps every slice of a small pie, largest first, and drops zero slices', () => {
    expect(foldPie(slices(1, 3, 0, 2)).map(s => s.label)).toEqual(['s1', 's3', 's0']);
  });

  it('shows nothing for an all-zero pie', () => {
    expect(foldPie(slices(0, 0))).toEqual([]);
  });

  it('folds two or more slices under 2% into Other, but not a single one', () => {
    expect(foldPie(slices(98.5, 1.5)).map(s => s.label)).toEqual(['s0', 's1']);
    expect(foldPie(slices(97, 1, 1, 1))).toEqual([{ label: 's0', value: 97 }, { label: 'Other', value: 3 }]);
  });

  it('caps the pie at eight slices including Other', () => {
    expect(foldPie(slices(8, 7, 6, 5, 4, 3, 2, 1)).map(s => s.label)).toHaveLength(8);
    const folded = foldPie(slices(9, 8, 7, 6, 5, 4, 3, 2, 1));
    expect(folded).toHaveLength(8);
    expect(folded[7]).toEqual({ label: 'Other', value: 3 });
  });

  it('merges an agent-provided Other slice into the folded one', () => {
    const data = [...slices(9, 8, 7, 6, 5, 4, 3, 2), { label: 'Other', value: 1 }];
    expect(foldPie(data).at(-1)).toEqual({ label: 'Other', value: 3 });
    expect(foldPie([{ label: 'Other', value: 1 }, { label: 'a', value: 2 }])).toEqual([{ label: 'a', value: 2 }, { label: 'Other', value: 1 }]);
  });
});

describe('series colors', () => {
  const pie = (id: string, labels: string[]): Component => ({ id, component: 'PieChart', label: id, data: labels.map((label, i) => ({ label, value: 10 - i })) });
  const line = (id: string, names: string[]): Component => ({ id, component: 'LineChart', label: id, x: { type: 'number', values: [1, 2] },
    series: names.map(name => ({ name, values: [1, 2] })) });

  it('keeps one slot per entity across charts by first appearance, with Other neutral', () => {
    const shared = assignSlots([pie('a', ['api', 'web']), { id: 't', component: 'Text', text: 'x' }, line('b', ['db', 'api', 'Other'])]);
    expect([...shared]).toEqual([['api', 0], ['web', 1], ['db', 2]]);
    expect(chartSlots(['db', 'api', 'Other'], shared)).toEqual([2, 0, -1]);
    expect(slotColor(2)).toBe('var(--series-3)');
    expect(slotColor(-1)).toBe('var(--series-other)');
  });

  it('falls back to chart order instead of repeating a color when a result has more than eight names', () => {
    const shared = assignSlots([line('a', ['1', '2', '3', '4', '5', '6', '7', '8']), line('b', ['9', '1'])]);
    expect(chartSlots(['1', '8'], shared)).toEqual([0, 7]);
    expect(chartSlots(['9', '1', 'Other'], shared)).toEqual([0, 1, -1]);
  });

  it('names only colored charts', () => {
    expect(chartNames({ id: 's', component: 'Stat', label: 'x', value: 1 })).toEqual([]);
    expect(chartNames({ id: 's', component: 'ScatterChart', label: 'x', series: [{ name: 'a', points: [[1, 2]] }] })).toEqual(['a']);
  });
});
