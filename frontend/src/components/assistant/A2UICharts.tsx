import type { ReactNode } from 'react';
import { Filler } from 'chart.js';
import type { ChartOptions, TooltipItem } from 'chart.js';
import { Bar, Doughnut, Line, Scatter } from 'react-chartjs-2';
import '../../pages/dashboard/chartSetup';
import type { ChartComponent, Scalar } from '../../lib/a2ui';
import { parseChartDate } from '../../lib/a2ui';
import { chartNames, chartSlots, foldPie, formatValue, slotColor } from '../../lib/a2uiCharts';
import { formatExactDate } from '../../lib/format';
import { ResultTable } from './A2UIWidgets';
import styles from './A2UIResult.module.css';

/** Canvas charts cannot resolve var(), so read the palette tokens at render. */
function token(color: string): string {
  const name = /^var\((--[\w-]+)\)$/.exec(color)?.[1];
  return name && typeof document !== 'undefined' ? getComputedStyle(document.documentElement).getPropertyValue(name).trim() : color;
}

function withAlpha(hex: string, alpha: number): string {
  return /^#[0-9a-f]{6}$/i.test(hex) ? hex + Math.round(alpha * 255).toString(16).padStart(2, '0') : hex;
}

function theme() {
  return { surface: token('var(--bg-card)'), tick: token('var(--subtext0)'), grid: token('var(--border)'), title: token('var(--subtext1)') };
}

/** Date-only axes stay in UTC; timed values use the viewer's zone. */
function dateFormatter(values: string[]): (ms: number) => string {
  if (values.some(value => value.includes('T'))) return ms => formatExactDate(ms);
  return ms => new Date(ms).toLocaleDateString('en-US', { timeZone: 'UTC', month: 'short', day: 'numeric', year: 'numeric' });
}

const MAX_DATE_TICKS = 8;

/** Dates tick on their data points (thinned evenly), never between days. */
function dateTicks(xs: number[]) {
  const step = Math.ceil(xs.length / MAX_DATE_TICKS);
  return (scale: { ticks: { value: number }[] }) => {
    scale.ticks = xs.filter((_, i) => i % step === 0).map(value => ({ value }));
  };
}

function axis(text: string | undefined, format: (value: number) => string, extra: object = {}) {
  const { tick, grid, title } = theme();
  return {
    ...extra,
    grid: { color: grid }, border: { color: grid },
    ticks: { color: tick, callback: (value: string | number) => format(Number(value)) },
    title: { display: !!text, text, color: title },
  };
}

function categoryAxis(text: string | undefined, extra: object = {}) {
  const { tick, grid, title } = theme();
  return { ...extra, grid: { display: false }, border: { color: grid }, ticks: { color: tick }, title: { display: !!text, text, color: title } };
}

type Tooltip<T extends 'bar' | 'line' | 'doughnut' | 'scatter'> = { label: (item: TooltipItem<T>) => string; title?: (items: TooltipItem<T>[]) => string };

function isGap(raw: unknown): boolean {
  return raw === null || (typeof raw === 'object' && (raw as { y?: unknown }).y === null);
}

/** Shared chart.js options: no built-in legend, and tooltips skip null gaps. */
function options<T extends 'bar' | 'line' | 'doughnut' | 'scatter'>(callbacks: Tooltip<T>, extra: object = {}): ChartOptions<T> {
  return {
    responsive: true, maintainAspectRatio: false, animation: false,
    plugins: { legend: { display: false }, tooltip: { callbacks, filter: (item: TooltipItem<T>) => !isGap(item.raw) } },
    ...extra,
  } as ChartOptions<T>;
}

function Legend({ names, slots }: { names: string[]; slots: number[] }) {
  return <ul className={styles.legend} aria-label="Legend">{names.map((name, i) => <li key={name}>
    <span className={styles.swatch} style={{ background: slotColor(slots[i]) }} aria-hidden="true" />{name}
  </li>)}</ul>;
}

function Figure({ label, names, slots, columns, rows, formatCell, children }: {
  label: string; names: string[]; slots: number[]; columns: string[]; rows: Scalar[][];
  formatCell?: (value: Scalar, column: number) => string; children: ReactNode;
}) {
  return <figure className={styles.chart}>
    <figcaption>{label}</figcaption>
    {children}
    {names.length >= 2 && <Legend names={names} slots={slots} />}
    <details className={styles.dataTable}>
      <summary>Data table</summary>
      <ResultTable columns={columns} rows={rows} formatCell={formatCell} />
    </details>
  </figure>;
}

function ariaLabel(label: string, description: string) {
  return `${label}. ${description}. Exact values are in the data table.`;
}

function count(n: number, noun: string, plural = `${noun}s`) {
  return `${n} ${n === 1 ? noun : plural}`;
}

type ViewProps<T> = { chart: T; names: string[]; slots: number[]; colors: string[]; value: (v: number) => string };
type Of<K extends ChartComponent['component']> = ViewProps<Extract<ChartComponent, { component: K }>>;

export function ResultChart({ chart, slots: shared }: { chart: ChartComponent; slots: Map<string, number> }) {
  const names = chartNames(chart);
  const slots = chartSlots(names, shared);
  const props = { names, slots, colors: slots.map(slot => token(slotColor(slot))), value: (v: number) => formatValue(v, { unit: chart.unit, format: chart.format }) };
  switch (chart.component) {
    case 'PieChart': return <PieView chart={chart} {...props} />;
    case 'MultiBarChart': return <BarsView chart={chart} {...props} />;
    case 'LineChart': return <LineView chart={chart} {...props} />;
    case 'ScatterChart': return <ScatterView chart={chart} {...props} />;
  }
}

function PieView({ chart, names, slots, colors, value }: Of<'PieChart'>) {
  const { surface } = theme();
  const visible = foldPie(chart.data);
  const total = chart.data.reduce((sum, item) => sum + item.value, 0);
  const share = (v: number) => `${(v / total * 100).toLocaleString(undefined, { maximumFractionDigits: 1 })}%`;
  const donut = chart.variant !== 'pie';
  const kind = donut ? 'Donut' : 'Pie';
  return <Figure label={chart.label} names={names} slots={slots} columns={['Label', 'Value', 'Share']}
    rows={[...chart.data].sort((a, b) => b.value - a.value).map(item => [item.label, item.value, total ? item.value / total * 100 : 0])}
    formatCell={(v, column) => column === 1 ? value(Number(v)) : column === 2 ? formatValue(Number(v), { format: 'percent' }) : String(v)}>
    {visible.length === 0 ? <p className={styles.caption}>No data</p> : <div className={styles.pie}>
      <Doughnut role="img" aria-label={ariaLabel(chart.label, `${kind} chart with ${count(visible.length, 'slice')}, total ${value(total)}`)}
        data={{ labels: names, datasets: [{ data: visible.map(item => item.value), backgroundColor: colors, borderColor: surface, borderWidth: 2 }] }}
        options={options<'doughnut'>({ label: item => `${item.label}: ${value(item.parsed)} (${share(item.parsed)})` }, { cutout: donut ? '62%' : 0 })} />
      {donut && <span className={styles.pieTotal}><strong>{value(total)}</strong><span className={styles.caption}>Total</span></span>}
    </div>}
  </Figure>;
}

function BarsView({ chart, names, slots, colors, value }: Of<'MultiBarChart'>) {
  const { surface } = theme();
  const stacked = chart.mode === 'stacked';
  const horizontal = chart.orientation === 'horizontal';
  const valueAxis = axis(chart.yLabel, value, { stacked, beginAtZero: true });
  const labelAxis = categoryAxis(chart.xLabel, { stacked });
  const height = horizontal ? Math.min(640, Math.max(200, chart.categories.length * (stacked ? 28 : 12 + 14 * chart.series.length) + 60)) : 260;
  return <Figure label={chart.label} names={names} slots={slots} columns={[chart.xLabel ?? 'Category', ...names]}
    rows={chart.categories.map((category, i) => [category, ...chart.series.map(s => s.values[i])])}
    formatCell={(v, column) => column === 0 ? String(v) : value(Number(v))}>
    <div className={styles.canvas} style={{ height }}>
      <Bar role="img" aria-label={ariaLabel(chart.label, `${stacked ? 'Stacked' : 'Grouped'} ${horizontal ? 'horizontal ' : ''}bar chart with ${count(names.length, 'series', 'series')} across ${count(chart.categories.length, 'category', 'categories')}`)}
        data={{ labels: chart.categories, datasets: chart.series.map((s, i) => ({
          label: s.name, data: s.values, backgroundColor: colors[i],
          borderColor: surface, borderWidth: stacked ? 1 : 0, borderRadius: 4, borderSkipped: 'start' as const,
        })) }}
        options={options<'bar'>({ label: item => `${item.dataset.label}: ${value(Number(horizontal ? item.parsed.x : item.parsed.y))}` }, {
          indexAxis: horizontal ? 'y' : 'x', interaction: { mode: 'index', intersect: false },
          scales: horizontal ? { y: labelAxis, x: valueAxis } : { x: labelAxis, y: valueAxis },
        })} />
    </div>
  </Figure>;
}

function LineView({ chart, names, slots, colors, value }: Of<'LineChart'>) {
  const { x } = chart;
  const formatX = x.type === 'date' ? dateFormatter(x.values) : x.type === 'number' ? (v: number) => v.toLocaleString() : undefined;
  const xs = x.type === 'date' ? x.values.map(parseChartDate) : x.values;
  const points = x.values.length <= 24;
  return <Figure label={chart.label} names={names} slots={slots} columns={[chart.xLabel ?? (x.type === 'date' ? 'Date' : 'X'), ...names]}
    rows={x.values.map((xv, i) => [xv, ...chart.series.map(s => s.values[i])])}
    formatCell={(v, column) => column > 0 ? value(Number(v)) : x.type === 'date' ? formatX!(parseChartDate(String(v))) : String(v)}>
    <div className={styles.canvas}>
      <Line role="img" aria-label={ariaLabel(chart.label, `${chart.area ? 'Area' : 'Line'} chart with ${count(names.length, 'series', 'series')} over ${count(x.values.length, 'point')}`)}
        plugins={chart.area ? [Filler] : []}
        data={{
          labels: x.type === 'category' ? x.values : undefined,
          datasets: chart.series.map((s, i) => ({
            label: s.name, borderColor: colors[i], backgroundColor: chart.area ? withAlpha(colors[i], 0.18) : colors[i],
            data: x.type === 'category' ? s.values : s.values.map((y, j) => ({ x: xs[j] as number, y })),
            borderWidth: 2, pointRadius: points ? 3 : 0, pointHoverRadius: 4, pointHitRadius: 8, fill: chart.area ? 'origin' : false,
          })),
        }}
        options={options<'line'>({
          label: item => `${item.dataset.label}: ${value(item.parsed.y ?? 0)}`,
          title: items => !items.length ? '' : formatX ? formatX(items[0].parsed.x ?? 0) : items[0].label,
        }, {
          interaction: { mode: 'index', intersect: false },
          scales: {
            x: formatX ? axis(chart.xLabel, formatX, { type: 'linear', min: xs[0], max: xs[xs.length - 1], ...(x.type === 'date' ? { afterBuildTicks: dateTicks(xs as number[]) } : {}) }) : categoryAxis(chart.xLabel),
            y: axis(chart.yLabel, value, { beginAtZero: !!chart.area }),
          },
        })} />
    </div>
  </Figure>;
}

function ScatterView({ chart, names, slots, colors, value }: Of<'ScatterChart'>) {
  const { surface } = theme();
  const formatX = (v: number) => formatValue(v, { unit: chart.xUnit });
  return <Figure label={chart.label} names={names} slots={slots} columns={['Series', chart.xLabel ?? 'X', chart.yLabel ?? 'Y']}
    rows={chart.series.flatMap(s => s.points.map(([px, py]) => [s.name, px, py]))}
    formatCell={(v, column) => column === 1 ? formatX(Number(v)) : column === 2 ? value(Number(v)) : String(v)}>
    <div className={styles.canvas}>
      <Scatter role="img" aria-label={ariaLabel(chart.label, `Scatter chart with ${count(names.length, 'series', 'series')}`)}
        data={{ datasets: chart.series.map((s, i) => ({
          label: s.name, data: s.points.map(([px, py]) => ({ x: px, y: py })), backgroundColor: colors[i],
          borderColor: surface, borderWidth: 2, pointRadius: 5, pointHoverRadius: 6, pointHitRadius: 8,
        })) }}
        options={options<'scatter'>({ label: item => `${item.dataset.label}: ${formatX(item.parsed.x ?? 0)}, ${value(item.parsed.y ?? 0)}` }, {
          scales: { x: axis(chart.xLabel, formatX), y: axis(chart.yLabel, value) },
        })} />
    </div>
  </Figure>;
}
