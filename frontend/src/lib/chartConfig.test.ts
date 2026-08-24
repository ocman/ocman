import { describe, it, expect } from 'vitest';
import {
  CHART_X_TICKS,
  CHART_COLORS,
  STOP_REASON_COLORS,
  BAR_OPTIONS_TOKS,
  BAR_OPTIONS_DURATION,
  BAR_OPTIONS_STACKED,
  BAR_OPTIONS_COST_BY_MODEL,
  BAR_OPTIONS_HOURLY_TOKENS,
  LINE_OPTIONS_COST,
  LINE_OPTIONS_CACHE,
  DOUGHNUT_OPTIONS,
} from './chartConfig';

describe('chartConfig', () => {
  it('exposes a populated colour palette and stop-reason palette', () => {
    expect(CHART_COLORS.length).toBeGreaterThan(0);
    expect(STOP_REASON_COLORS.length).toBeGreaterThan(0);
    for (const c of [...CHART_COLORS, ...STOP_REASON_COLORS]) {
      expect(c).toMatch(/^#[0-9a-f]{6}$/i);
    }
  });

  it('uses the shared x-tick configuration on time-series charts', () => {
    expect(BAR_OPTIONS_TOKS.scales.x.ticks).toBe(CHART_X_TICKS);
    expect(BAR_OPTIONS_DURATION.scales.x.ticks).toBe(CHART_X_TICKS);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.x.ticks).toBe(CHART_X_TICKS);
    expect(LINE_OPTIONS_COST.scales.x.ticks).toBe(CHART_X_TICKS);
    expect(LINE_OPTIONS_CACHE.scales.x.ticks).toBe(CHART_X_TICKS);
  });

  it('formats Tok/s on the y-axis of BAR_OPTIONS_TOKS', () => {
    const cb = BAR_OPTIONS_TOKS.scales.y.ticks.callback as (v: number) => string;
    expect(cb(42)).toBe('42Tok/s');
  });

  it('formats seconds on BAR_OPTIONS_DURATION', () => {
    const cb = BAR_OPTIONS_DURATION.scales.y.ticks.callback as (v: number) => string;
    expect(cb(5)).toBe('5s');
  });

  it('caps cache-efficiency line chart at 100', () => {
    expect(LINE_OPTIONS_CACHE.scales.y.max).toBe(100);
  });

  it('renders the doughnut legend on the right', () => {
    expect(DOUGHNUT_OPTIONS.plugins.legend.position).toBe('right');
  });

  it('uses stacked y-axes on the stacked bar charts', () => {
    expect(BAR_OPTIONS_STACKED.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_STACKED.scales.y.stacked).toBe(true);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_COST_BY_MODEL.scales.y.stacked).toBe(true);
    expect(BAR_OPTIONS_HOURLY_TOKENS.scales.x.stacked).toBe(true);
    expect(BAR_OPTIONS_HOURLY_TOKENS.scales.y.stacked).toBe(true);
  });

  it('disables animation on every chart preset (deterministic rendering)', () => {
    for (const opts of [
      BAR_OPTIONS_TOKS,
      BAR_OPTIONS_DURATION,
      BAR_OPTIONS_STACKED,
      BAR_OPTIONS_COST_BY_MODEL,
      BAR_OPTIONS_HOURLY_TOKENS,
      LINE_OPTIONS_COST,
      LINE_OPTIONS_CACHE,
      DOUGHNUT_OPTIONS,
    ]) {
      expect(opts.animation).toBe(false);
      expect(opts.responsive).toBe(true);
      expect(opts.maintainAspectRatio).toBe(false);
    }
  });
});
