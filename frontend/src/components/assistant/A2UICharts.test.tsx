// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { BarElement, Interaction } from 'chart.js';
import type { Chart, ChartEvent } from 'chart.js';
import { A2UIResult } from './A2UIResult';
import { chartResult } from '../../test/a2uiFixture';

type Captured = { type: string; props: Record<string, any> }; // eslint-disable-line @typescript-eslint/no-explicit-any
const charts = vi.hoisted(() => [] as Captured[]);
vi.mock('react-chartjs-2', () => {
  const make = (type: string) => (props: Captured['props']) => {
    charts.push({ type, props });
    return <canvas role={props.role} aria-label={props['aria-label']} />;
  };
  return { Bar: make('bar'), Doughnut: make('doughnut'), Line: make('line'), Scatter: make('scatter') };
});

const PALETTE = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#1f9a1f', '#9085e9', '#e66767'];

beforeAll(() => {
  PALETTE.forEach((hex, i) => document.documentElement.style.setProperty(`--series-${i + 1}`, hex));
  document.documentElement.style.setProperty('--series-other', '#7f849c');
});
beforeEach(() => { charts.length = 0; });

const last = (label: string) => charts.findLast(c => String(c.props['aria-label']).startsWith(`${label}.`))!;
const tooltip = (chart: Captured) => chart.props.options.plugins.tooltip;

async function renderCharts(result = chartResult()) {
  render(<A2UIResult result={result} />);
  await screen.findByRole('img', { name: /^Requested vs used\./ });
}

describe('A2UI charts', () => {
  it('renders captioned charts with accessible names, legends and data tables', async () => {
    await renderCharts();
    expect(screen.getByRole('img', { name: 'CPU requested. Donut chart with 4 slices, total 8 cores. Exact values are in the data table.' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /^Memory requested\. Pie chart with 4 slices, total 24 GiB/ })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /^CPU used per day\. Area chart with 2 series over 3 points/ })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /^CPU by app\. Stacked horizontal bar chart with 2 series across 4 categories/ })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: /^Requested vs used\. Scatter chart with 2 series/ })).toBeInTheDocument();

    const pie = screen.getByText('CPU requested', { selector: 'figcaption' }).closest('figure')!;
    expect(within(pie).getByText('8 cores')).toBeInTheDocument(); // donut total
    expect(within(within(pie).getByRole('list', { name: 'Legend' })).getAllByRole('listitem').map(li => li.textContent)).toEqual(['api', 'worker', 'web', 'cron']);
    fireEvent.click(within(pie).getByText('Data table'));
    const rows = within(within(pie).getByRole('table')).getAllByRole('row');
    expect(rows[1]).toHaveTextContent('api4 cores50%');

    const line = screen.getByText('CPU used per day', { selector: 'figcaption' }).closest('figure')!;
    const lineRows = within(within(line).getByRole('table')).getAllByRole('row');
    expect(within(line).getAllByRole('columnheader').map(th => th.querySelector('button span')?.textContent)).toEqual(['Date', 'worker', 'api']);
    expect(lineRows[1]).toHaveTextContent('Nov 1, 2026');
    expect(lineRows[2]).toHaveTextContent('Nov 2, 20263 cores');

    // A single-series chart needs no legend.
    const single = chartResult();
    const components = (single.messages[1] as { updateComponents: { components: { id: string; series?: unknown[] }[] } }).updateComponents.components;
    components.find(c => c.id === 'usage')!.series!.splice(1);
    charts.length = 0;
    const { container } = render(<A2UIResult result={single} />);
    await within(container).findByRole('img', { name: /^CPU used per day\. Area chart with 1 series/ });
    expect(within(within(container).getByText('CPU used per day', { selector: 'figcaption' }).closest('figure')!).queryByRole('list', { name: 'Legend' })).toBeNull();
  });

  it('shares entity colors except for scatter, which uses the pairwise-safe trio', async () => {
    await renderCharts();
    const colorsByName = (chart: Captured) => Object.fromEntries((chart.props.data.labels as string[])
      .map((label, i) => [label, chart.props.data.datasets[0].backgroundColor[i]]));
    const requested = colorsByName(last('CPU requested'));
    expect(requested).toEqual({ api: PALETTE[0], worker: PALETTE[1], web: PALETTE[2], cron: PALETTE[3] });
    expect(colorsByName(last('Memory requested'))).toEqual(requested);
    const series = (label: string) => Object.fromEntries(last(label).props.data.datasets.map((d: { label: string; borderColor: string; backgroundColor: string }) =>
      [d.label, label === 'CPU used per day' ? d.borderColor : d.backgroundColor]));
    expect(series('CPU used per day')).toEqual({ worker: PALETTE[1], api: PALETTE[0] });
    expect(series('Requested vs used')).toEqual({ api: PALETTE[0], web: PALETTE[1] });
    // Series names new to the result take the next free slots.
    expect(series('CPU by app')).toEqual({ requested: PALETTE[4], used: PALETTE[5] });
    const swatches = screen.getByText('CPU by app', { selector: 'figcaption' }).closest('figure')!.querySelectorAll('li > span');
    expect([...swatches].map(s => (s as HTMLElement).style.background)).toEqual(['var(--series-5)', 'var(--series-6)']);
    expect(last('CPU used per day').props.data.datasets[0].fill).toBe('origin');
    expect(last('CPU used per day').props.data.datasets[1].backgroundColor).toBe(`${PALETTE[0]}2e`);
    expect(last('CPU used per day').props.plugins).toHaveLength(1);
  });

  it('keeps scatter colors pairwise safe after earlier charts allocate six slots', async () => {
    const result = chartResult();
    const components = (result.messages[1] as { updateComponents: { components: Record<string, unknown>[] } }).updateComponents.components;
    components.find(c => c.id === 'scatter')!.series = ['cron', 'used', 'Other'].map(name => ({ name, points: [[1, 2]] }));
    await renderCharts(result);
    expect(last('Requested vs used').props.data.datasets.map((d: { backgroundColor: string }) => d.backgroundColor)).toEqual([PALETTE[0], PALETTE[1], '#7f849c']);
    const figure = screen.getByText('Requested vs used', { selector: 'figcaption' }).closest('figure')!;
    expect([...figure.querySelectorAll('li > span')].map(s => (s as HTMLElement).style.background)).toEqual(['var(--series-1)', 'var(--series-2)', 'var(--series-other)']);
  });

  it.each(['horizontal', 'vertical'])('selects tooltip categories along the %s index axis', async orientation => {
    const result = chartResult();
    const components = (result.messages[1] as { updateComponents: { components: Record<string, unknown>[] } }).updateComponents.components;
    Object.assign(components.find(c => c.id === 'bars')!, { orientation, categories: ['A', 'B'], series: [{ name: 'used', values: [100, 10] }] });
    await renderCharts(result);
    const horizontal = orientation === 'horizontal';
    const data = [100, 10].map((value, i) => new BarElement({
      x: horizontal ? value : 20 + i * 40, y: horizontal ? 20 + i * 40 : value,
      base: 0, width: horizontal ? value : 20, height: horizontal ? 20 : value, horizontal,
    }));
    const chart = { getSortedVisibleDatasetMetas: () => [{ index: 0, data, controller: { _cachedMeta: { iScale: { axis: horizontal ? 'y' : 'x' } } } }], isPointInArea: () => true } as unknown as Chart;
    const event = { native: {}, x: horizontal ? 10 : 20, y: horizontal ? 20 : 10 } as ChartEvent;
    const selected = Interaction.modes.index(chart, event, last('CPU by app').props.options.interaction, false);
    expect(selected.map(item => item.index)).toEqual([0]);
  });

  it('formats tooltips and axes with the unit and skips null gaps', async () => {
    await renderCharts();
    const pie = tooltip(last('Memory requested'));
    expect(pie.callbacks.label({ label: 'api', parsed: 12884901888 })).toBe('api: 12 GiB (50%)');

    const line = last('CPU used per day');
    const day = Date.UTC(2026, 10, 2);
    expect(line.props.data.datasets[0].data[1]).toEqual({ x: day, y: null });
    expect(tooltip(line).filter({ raw: { x: day, y: null } })).toBe(false);
    expect(tooltip(line).filter({ raw: { x: day, y: 3 } })).toBe(true);
    expect(tooltip(line).callbacks.title([{ parsed: { x: day } }])).toBe('Nov 2, 2026');
    expect(tooltip(line).callbacks.title([])).toBe('');
    expect(tooltip(line).callbacks.label({ dataset: { label: 'api' }, parsed: { y: 3 } })).toBe('api: 3 cores');
    expect(line.props.options.scales.x).toMatchObject({ type: 'linear', min: Date.UTC(2026, 10, 1), max: Date.UTC(2026, 10, 3) });
    expect(line.props.options.scales.y.ticks.callback(2.5)).toBe('2.5 cores');
    expect(line.props.options.scales.y.beginAtZero).toBe(true);
    const scale = { ticks: [{ value: 0.5 }] };
    line.props.options.scales.x.afterBuildTicks(scale);
    expect(scale.ticks).toEqual([Date.UTC(2026, 10, 1), Date.UTC(2026, 10, 2), Date.UTC(2026, 10, 3)].map(value => ({ value })));

    const bars = last('CPU by app');
    expect(bars.props.options).toMatchObject({ indexAxis: 'y', scales: { x: { stacked: true }, y: { stacked: true, title: { text: 'App' } } } });
    expect(tooltip(bars).filter({ raw: null })).toBe(false);
    expect(tooltip(bars).callbacks.label({ dataset: { label: 'used' }, parsed: { x: 0.4, y: 3 } })).toBe('used: 0.4 cores');

    const scatter = last('Requested vs used');
    expect(tooltip(scatter).callbacks.label({ dataset: { label: 'web' }, parsed: { x: 1, y: 0.7 } })).toBe('web: 1 cores, 0.7 cores');
    expect(scatter.props.data.datasets[1].data).toEqual([{ x: 1, y: 0.7 }, { x: 0.5, y: 0.4 }]);
  });

  it('renders vertical grouped bars, category and numeric lines, timed dates and empty pies', async () => {
    const result = chartResult();
    const components = (result.messages[1] as { updateComponents: { components: Record<string, unknown>[] } }).updateComponents.components;
    const set = (id: string, patch: Record<string, unknown>) => Object.assign(components.find(c => c.id === id)!, patch);
    set('bars', { mode: undefined, orientation: undefined });
    set('cpuUsed', { data: [{ label: 'api', value: 0 }] });
    set('usage', { area: false, x: { type: 'category', values: ['Mon', 'Tue', 'Wed'] } });
    set('scatter', { series: [{ name: 'api', points: [[1, 2]] }] });
    components.push({ id: 'timed', component: 'LineChart', label: 'Timed', x: { type: 'date', values: ['2026-11-01T10:00Z', '2026-11-01T11:00Z'] }, series: [{ name: 'api', values: [1, 2] }] },
      { id: 'numbers', component: 'LineChart', label: 'Numbers', xLabel: 'Load', x: { type: 'number', values: [1000, 2000] }, series: [{ name: 'api', values: [1, 2] }] });
    (components[0].children as string[]).push('timed', 'numbers');
    await renderCharts(result);

    expect(screen.getByRole('img', { name: /^CPU by app\. Grouped bar chart/ })).toBeInTheDocument();
    expect(last('CPU by app').props.options).toMatchObject({ indexAxis: 'x', scales: { x: { stacked: false }, y: { stacked: false } } });
    expect(tooltip(last('CPU by app')).callbacks.label({ dataset: { label: 'used' }, parsed: { x: 0, y: 3 } })).toBe('used: 3 cores');

    const empty = screen.getByText('CPU used share', { selector: 'figcaption' }).closest('figure')!;
    expect(within(empty).getByText('No data')).toBeInTheDocument();
    expect(within(empty).queryByRole('img')).toBeNull();

    const category = last('CPU used per day');
    expect(category.props.data.labels).toEqual(['Mon', 'Tue', 'Wed']);
    expect(category.props.data.datasets[0]).toMatchObject({ data: [1.9, null, 2.2], fill: false, backgroundColor: PALETTE[1] });
    expect(category.props.plugins).toEqual([]);
    expect(tooltip(category).callbacks.title([{ label: 'Tue' }])).toBe('Tue');
    expect(screen.getByRole('img', { name: /^CPU used per day\. Line chart/ })).toBeInTheDocument();

    expect(tooltip(last('Timed')).callbacks.title([{ parsed: { x: Date.UTC(2026, 10, 1, 10) } }])).toMatch(/^Nov 1, \d{2}:00$/);
    expect(last('Numbers').props.options.scales.x.ticks.callback(1000)).toBe('1,000');
    expect(screen.getByText('Numbers', { selector: 'figcaption' }).closest('figure')!.querySelector('th')).toHaveTextContent('Load');
    expect(screen.getByRole('img', { name: /^Requested vs used\. Scatter chart with 1 series\./ })).toBeInTheDocument();
  });

  it('renders stat tiles with formatted values and directional deltas that do not rely on color', async () => {
    await renderCharts();
    const tile = (label: string) => screen.getByText(label, { selector: 'figcaption' }).closest('figure')!;
    const cpu = tile('CPU used');
    expect(cpu).toHaveTextContent('6.4 cores');
    expect(within(cpu).getByText(/\+0\.8 cores vs last week/)).toHaveTextContent('▲ +0.8 cores vs last week (worse)');
    expect(within(cpu).getByText('▲')).toHaveAttribute('aria-hidden', 'true');
    expect(tile('Memory total')).toHaveTextContent('24 GiB');
    expect(tile('Error rate')).toHaveTextContent('1.5%▼ -0.5% (better)');
  });

  it('formats the single-series BarChart with its unit hint', () => {
    const result = chartResult();
    const update = (result.messages[1] as { updateComponents: { components: unknown[] } }).updateComponents;
    update.components = [{ id: 'root', component: 'BarChart', label: 'Memory', format: 'bytes', data: [{ label: 'api', value: 1536 }] }];
    render(<A2UIResult result={result} />);
    expect(screen.getByText('1.5 KiB')).toBeInTheDocument();
  });
});
