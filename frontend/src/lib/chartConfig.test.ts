// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';
import {
  BAR_OPTIONS_TOKS,
  BAR_OPTIONS_DURATION,
  BAR_OPTIONS_WAIT_TIME,
  BAR_OPTIONS_STACKED,
  BAR_OPTIONS_COST_BY_MODEL,
  BAR_OPTIONS_SESSIONS,
  BAR_OPTIONS_TOKENS_BY_MODEL,
  BAR_OPTIONS_HOURLY_TOKENS,
  LINE_OPTIONS_CACHE,
  LINE_OPTIONS_DATABASE_SIZE,
  DOUGHNUT_OPTIONS,
} from './chartConfig';

describe('chartConfig', () => {
  it.each([LINE_OPTIONS_CACHE, LINE_OPTIONS_DATABASE_SIZE])('inspects line charts by horizontal position without hitting a point', (options) => {
    expect(options).toHaveProperty('interaction', { mode: 'nearest', axis: 'x', intersect: false });
  });

  it('reads its palettes and legend colour from the theme tokens', async () => {
    const style = document.createElement('style');
    style.textContent = readFileSync('src/tokens.css', 'utf8');
    document.head.appendChild(style);
    try {
      vi.resetModules();
      const themed = await import('./chartConfig');
      expect(themed.CHART_COLORS).toEqual(['#89b4fa', '#a6e3a1', '#cba6f7', '#fab387', '#f38ba8', '#74c7ec', '#94e2d5', '#f9e2af']);
      expect(themed.STOP_REASON_COLORS).toHaveLength(7);
      expect(themed.DOUGHNUT_OPTIONS.plugins.legend.labels.color).toBe('#bac2de');
      expect(themed.BAR_OPTIONS_SESSIONS.plugins.legend.labels.color).toBe('#bac2de');
    } finally {
      style.remove();
    }
  });

  it('leaves colours unset outside a document', async () => {
    vi.stubGlobal('document', undefined);
    try {
      vi.resetModules();
      const bare = await import('./chartConfig');
      expect(bare.CHART_COLORS.every((color) => color === '')).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('uses the shared x-tick configuration on time-series charts', () => {
    const ticks = BAR_OPTIONS_TOKS.scales.x.ticks;
    expect(ticks).toEqual({ maxTicksLimit: 10, maxRotation: 45, minRotation: 45 });
    expect(BAR_OPTIONS_DURATION.scales.x.ticks).toBe(ticks);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.x.ticks).toBe(ticks);
    expect(LINE_OPTIONS_CACHE.scales.x.ticks).toBe(ticks);
  });

  it('formats Tok/s on the y-axis of BAR_OPTIONS_TOKS', () => {
    const cb = BAR_OPTIONS_TOKS.scales.y.ticks.callback as (v: number) => string;
    expect(cb(42)).toBe('42Tok/s');
  });

  it('formats seconds on BAR_OPTIONS_DURATION', () => {
    const cb = BAR_OPTIONS_DURATION.scales.y.ticks.callback as (v: number) => string;
    expect(cb(5)).toBe('5s');
  });

  it('stacks waiting time and formats duration tooltips', () => {
    expect(BAR_OPTIONS_WAIT_TIME.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_WAIT_TIME.scales.y.stacked).toBe(true);
    const label = BAR_OPTIONS_WAIT_TIME.plugins.tooltip.callbacks.label;
    expect(label({ dataset: { label: 'Tools' }, parsed: { y: 90 } })).toBe('Tools: 1m 30s');
    expect(label({ dataset: {}, parsed: { y: null } })).toBe(': 0s');
  });

  it('caps cache-efficiency line chart at 100', () => {
    expect(LINE_OPTIONS_CACHE.scales.y.max).toBe(100);
  });

  it('renders the doughnut legend on the right', () => {
    expect(DOUGHNUT_OPTIONS.plugins.legend.position).toBe('right');
  });

  it('renders Cartesian chart legends below the plot', () => {
    for (const opts of [
      BAR_OPTIONS_DURATION,
      BAR_OPTIONS_STACKED,
      BAR_OPTIONS_COST_BY_MODEL,
      BAR_OPTIONS_SESSIONS,
      BAR_OPTIONS_TOKENS_BY_MODEL,
      BAR_OPTIONS_HOURLY_TOKENS,
    ]) {
      expect(opts.plugins.legend.position).toBe('bottom');
    }
  });

  it('uses stacked y-axes on the stacked bar charts', () => {
    expect(BAR_OPTIONS_STACKED.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_STACKED.scales.y.stacked).toBe(true);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.y.stacked).toBe(true);
    expect(BAR_OPTIONS_HOURLY_TOKENS.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_HOURLY_TOKENS.scales.y.stacked).toBe(true);
  });

  it('shows the stacked total in tooltips', () => {
    const verticalFooter = BAR_OPTIONS_STACKED.plugins.tooltip.callbacks.footer;
    const horizontalFooter = BAR_OPTIONS_TOKENS_BY_MODEL.plugins.tooltip.callbacks.footer;
    expect(verticalFooter([{ parsed: { y: 3 } }, { parsed: { y: 7 } }])).toBe('Total: 10');
    expect(horizontalFooter([{ parsed: { x: 4 } }, { parsed: { x: 6 } }])).toBe('Total: 10');
    expect(BAR_OPTIONS_STACKED.interaction.mode).toBe('index');
  });

  it('hides zero-cost model entries from daily cost tooltips', () => {
    const filter = BAR_OPTIONS_COST_BY_MODEL.plugins.tooltip.filter;
    expect(filter({ parsed: { y: 0 } })).toBe(false);
    expect(filter({ parsed: { y: 0.01 } })).toBe(true);
  });

  it('disables animation and fills the card for every chart', async () => {
    const { Chart } = await import('chart.js');
    await import('../pages/dashboard/chartSetup');
    expect(Chart.defaults.animation).toBe(false);
    expect(Chart.defaults.responsive).toBe(true);
    expect(Chart.defaults.maintainAspectRatio).toBe(false);
  });
});
