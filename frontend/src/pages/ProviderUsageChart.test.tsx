// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ProviderUsageChart } from './ProviderUsageChart';

const chart = vi.hoisted(() => vi.fn());
vi.mock('react-chartjs-2', () => ({ Line: (props: { 'aria-label': string }) => {
  chart(props);
  return <div role="img" aria-label={props['aria-label']} />;
} }));

describe('ProviderUsageChart', () => {
  beforeEach(() => vi.resetAllMocks());

  it('plots timestamped percentages with gaps and an accessible name', () => {
    const data = [{ x: 1793548800000, y: 20 }, { x: 1793549100000, y: null }, { x: 1793549400000, y: 30 }];
    render(<ProviderUsageChart name="Codex" series={[{ key: 'window:5 hours', label: '5 hours', data }]} />);
    expect(screen.getByRole('img', { name: 'Codex subscription usage history' })).toBeInTheDocument();
    const props = chart.mock.lastCall?.[0];
    expect(props.data.datasets[0]).toMatchObject({ label: '5 hours', data, spanGaps: false, pointRadius: 2 });
    expect(props.options.scales.x.ticks.maxTicksLimit).toBe(5);
    expect(props.options.scales.x.ticks.callback(data[0].x)).toContain('Nov');
    expect(props.options.scales.y).toMatchObject({ min: 0, max: 100 });
    expect(props.options.scales.y.ticks.callback(25)).toBe('25%');
    const tooltip = props.options.plugins.tooltip.callbacks;
    expect(tooltip.title([])).toBe('');
    expect(tooltip.title([{ parsed: { x: data[0].x } }])).toBe(new Date(data[0].x).toLocaleString());
    expect(tooltip.label({ dataset: { label: '5 hours' }, parsed: { y: 20 } })).toBe('5 hours: 20% used');
  });
});
